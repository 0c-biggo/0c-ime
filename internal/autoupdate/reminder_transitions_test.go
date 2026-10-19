package autoupdate

import (
	"errors"
	"testing"
	"time"
)

func TestReminderReasonChangesCannotRepeatSameVersionWithinDay(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), shellErr: ErrNeedsConsent}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if len(p.notices) != 1 {
		t.Fatal("missing positive control")
	}
	clk.add(time.Hour)
	co.remind(s, clk.now()) // unchanged reason must remain quiet
	s.block(clk.now(), "needs_relogin", "installed; login pending")
	co.remind(s, clk.now())
	s.block(clk.now(), "needs_consent", "consent again")
	co.remind(s, clk.now())
	if len(p.notices) != 1 {
		t.Fatal("reason changes repeated the same version", p.notices)
	}
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(co.Base) // a new process must retain the last successful version
	co.remind(s, clk.now())
	if len(p.notices) != 1 {
		t.Fatal("restart bypassed the limit")
	}
	clk.add(23 * time.Hour)
	co.remind(s, clk.now())
	if len(p.notices) != 2 {
		t.Fatal("missing due reminder")
	}
}

func TestLongPreparedWaitSurvivesAlternatingBlockReasons(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), shellErr: ErrShellBusy}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	prepared := s.PreparedAt
	for h := 1; h <= 25; h++ {
		clk.add(time.Hour)
		if h%2 == 0 {
			p.shellErr = ErrShellBusy
		} else {
			p.shellErr = errors.New("artificial temporary installer failure")
		}
		s = run(t, co)
		if !s.PreparedAt.Equal(prepared) || s.Stage != StagePrepared {
			t.Fatal("lost pending package")
		}
		if h < 24 && len(p.notices) != 0 {
			t.Fatal("early reminder")
		}
	}
	if len(p.notices) != 1 {
		t.Fatal("changing reasons starved or repeated the long-wait reminder", p.notices)
	}
}

type failingReminder struct {
	*fakePlatform
	failures int
}

func (p *failingReminder) Notify(n Notice) error {
	if p.failures > 0 {
		p.failures--
		return errors.New("artificial notify failure")
	}
	return p.fakePlatform.Notify(n)
}

func TestFailedNewVersionNoticeDoesNotMislabelPreviousSuccessfulNotice(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), shellErr: ErrNeedsConsent}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	clk.add(time.Hour)
	s.Target.Version = "0.1.0-dev.42"
	n := &failingReminder{p, 1}
	co.Platform = n
	co.remind(s, clk.now())
	if s.NoticeFor != "0.1.0-dev.41/needs_consent" {
		t.Fatal("failed delivery relabeled the last successful notification", s.NoticeFor)
	}
	co.remind(s, clk.now())
	if len(p.notices) != 2 || s.NoticeFor != "0.1.0-dev.42/needs_consent" {
		t.Fatal("failure prevented prompt retry for the new version", p.notices)
	}
}

func TestSimulationReminderObserverRejectsChangedTextForSameVersion(t *testing.T) {
	w := &world{now: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), lastNotice: map[string]time.Time{}, requestedReminders: map[string]time.Time{}}
	p := simPlatform{w}
	n := Notice{Title: "人工待辦", Body: "等待閒置", TargetVersion: ver(41), TargetSHA256: "artificial"}
	p.Notify(n)
	w.now = w.now.Add(time.Hour)
	n.Title, n.Body = "人工新原因", "等待確認"
	p.Notify(n)
	if len(w.violations) != 1 {
		t.Fatal("changed text concealed a same-version duplicate")
	}
	w.violations = nil
	n.TargetVersion = ver(42)
	p.Notify(n)
	if len(w.violations) != 0 {
		t.Fatal("different version incorrectly throttled")
	}
}
