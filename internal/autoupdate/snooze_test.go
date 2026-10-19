package autoupdate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func pendingSnooze(t *testing.T) (*Coordinator, *clock, *fakePlatform, *State) {
	t.Helper()
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "core", 100)}
	co, clk := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode = ModeAsk
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	s = run(t, co)
	if len(p.notices) != 1 || s.Stage != StagePrepared {
		t.Fatal("missing positive control", s, p.notices)
	}
	return co, clk, p, s
}

func TestSnoozeNoticePersistsAndRemindsOnceAtOneHour(t *testing.T) {
	co, clk, p, s := pendingSnooze(t)
	p.calls = nil
	if err := SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now()); err != nil {
		t.Fatal(err)
	}
	loaded, _ := Load(co.Base)
	if loaded.Snooze == nil || loaded.Mode != ModeAsk || loaded.Stage != StagePrepared || len(p.calls) != 0 {
		t.Fatal("snooze must persist without install or policy change", loaded)
	}
	// Fresh coordinators have no in-memory reminder state; disk is authoritative.
	restart := func() *State {
		fresh := &Coordinator{Base: co.Base, Platform: p, Now: clk.now, Jitter: co.Jitter}
		return run(t, fresh)
	}
	clk.add(time.Hour - time.Nanosecond)
	if restart().Snooze == nil || len(p.notices) != 1 {
		t.Fatal("reminded early")
	}
	clk.add(time.Nanosecond)
	if restart().Snooze != nil || len(p.notices) != 2 {
		t.Fatal("missing exact one-hour reminder")
	}
	clk.add(time.Minute)
	if restart().Snooze != nil || len(p.notices) != 2 {
		t.Fatal("repeated after restart")
	}
	for _, call := range p.calls {
		if call != "fetch" {
			t.Fatal("snooze granted consent", p.calls)
		}
	}
}

func TestSnoozeNoticeRejectsStaleOrUnavailableTarget(t *testing.T) {
	for _, kind := range []string{"version", "asset", "active", "switched", "failed", "missing", "empty-hash"} {
		t.Run(kind, func(t *testing.T) {
			co, clk, _, s := pendingSnooze(t)
			version, hash := s.Target.Version, s.Target.SHA256
			switch kind {
			case "version":
				s.Target.Version = "0.1.0-dev.42"
			case "asset":
				s.Target.SHA256 = strings.Repeat("b", 64)
			case "active":
				s.Stage = StageActive
			case "switched":
				s.Switched = true
			case "failed":
				s.Failed = []string{version}
			case "missing":
				s.Target = nil
			case "empty-hash":
				hash = ""
			}
			if err := Save(co.Base, s); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(Path(co.Base))
			if err := SnoozeNotice(co.Base, version, hash, clk.now()); !errors.Is(err, ErrTargetChanged) {
				t.Fatal("accepted stale reminder", err)
			}
			after, _ := os.ReadFile(Path(co.Base))
			if string(before) != string(after) {
				t.Fatal("rejected notification changed state")
			}
		})
	}
}

type snoozeNotifier struct {
	*fakePlatform
	reject bool
}

func (p *snoozeNotifier) Notify(n Notice) error {
	if p.reject {
		return errors.New("artificial notification service unavailable")
	}
	return p.fakePlatform.Notify(n)
}

func TestSnoozeNoticeRetriesDeliveryFailureWithoutRepeatingSuccess(t *testing.T) {
	co, clk, p, s := pendingSnooze(t)
	if err := SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now()); err != nil {
		t.Fatal(err)
	}
	n := &snoozeNotifier{fakePlatform: p, reject: true}
	co.Platform = n
	clk.add(time.Hour)
	if run(t, co).Snooze == nil || len(p.notices) != 1 {
		t.Fatal("failure consumed reminder")
	}
	n.reject = false
	if run(t, co).Snooze != nil || len(p.notices) != 2 {
		t.Fatal("missing recovered reminder")
	}
	if run(t, co).Snooze != nil || len(p.notices) != 2 {
		t.Fatal("repeated successful reminder")
	}
}

func TestSnoozeNoticeNeverPostponesSafeAutomaticUpdate(t *testing.T) {
	co, clk, p, s := pendingSnooze(t)
	if err := SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now()); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(co.Base)
	s.Mode = ModeAuto
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	p.cond = Conditions{Idle: time.Hour, IdleKnown: true}
	if s = run(t, co); s.Stage != StageActive || s.Snooze != nil || len(p.notices) != 1 {
		t.Fatal("snooze postponed or repeated a safe update", s)
	}
}

func TestSnoozeNoticeDoesNotCarryToReplacedAssetOrWithdrawnVersion(t *testing.T) {
	for _, kind := range []string{"new-version", "new-asset", "withdrawn"} {
		t.Run(kind, func(t *testing.T) {
			co, clk, p, s := pendingSnooze(t)
			if err := SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now()); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "new-version":
				p.feed = feedFor("0.1.0-dev.42", "core", 100)
			case "new-asset":
				p.feed.Assets[0].SHA256 = strings.Repeat("b", 64)
			case "withdrawn":
				p.feed = feedFor("0.1.0-dev.41", "core", 100, "0.1.0-dev.41")
			}
			co.Force = true
			if s = run(t, co); s.Snooze != nil {
				t.Fatal("stale snooze retained", s.Snooze)
			}
		})
	}
}

func TestSnoozeNoticeObeysPauseAndCoordinatorLock(t *testing.T) {
	co, clk, p, s := pendingSnooze(t)
	unlock, err := lock(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	err = SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now())
	unlock()
	if err == nil {
		t.Fatal("snooze wrote during an updater transaction")
	}
	if err = SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now()); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(co.Base)
	s.PausedUntil = clk.now().Add(2 * time.Hour)
	if err = Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	clk.add(time.Hour)
	if run(t, co).Snooze == nil || len(p.notices) != 1 {
		t.Fatal("snooze bypassed user pause")
	}
	clk.add(time.Hour)
	if run(t, co).Snooze != nil || len(p.notices) != 2 {
		t.Fatal("pending reminder lost when pause ended")
	}
	_, err = co.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreferenceEditCannotEraseConcurrentSnooze(t *testing.T) {
	co, clk, _, s := pendingSnooze(t)
	loaded, release := make(chan struct{}), make(chan struct{})
	preferenceDone := make(chan error, 1)
	go func() {
		_, err := EditState(co.Base, func(state *State) error {
			close(loaded)
			<-release // snapshot was read before the snooze request
			state.Mode = ModeManual
			return nil
		})
		preferenceDone <- err
	}()
	<-loaded
	snoozeDone := make(chan error, 1)
	go func() { snoozeDone <- SnoozeNotice(co.Base, s.Target.Version, s.Target.SHA256, clk.now()) }()
	close(release)
	if err := <-preferenceDone; err != nil {
		t.Fatal(err)
	}
	if err := <-snoozeDone; err != nil {
		t.Fatal(err)
	}
	final, err := Load(co.Base)
	if err != nil || final.Mode != ModeManual || final.Snooze == nil {
		t.Fatal("a stale preference snapshot erased the new reminder", final, err)
	}
}
