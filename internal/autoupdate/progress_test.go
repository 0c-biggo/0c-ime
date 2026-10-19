package autoupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func foundTarget(t *testing.T, co *Coordinator) *State {
	t.Helper()
	s, err := co.Check(context.Background())
	if err != nil || s.Target == nil {
		t.Fatalf("%+v %v", s, err)
	}
	return s
}

func TestDownloadNowInManualModePreparesWithoutInstalling(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellErr: ErrNeedsConsent}
	co, _ := newCoordinator(t, p)
	s := foundTarget(t, co)
	s.Mode, s.PausedUntil = ModeManual, time.Now().Add(time.Hour)
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	p.calls = nil
	s, err := co.DownloadNow(context.Background(), s.Target.Version, s.Target.SHA256)
	if err != nil || s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "needs_consent" {
		t.Fatalf("%+v %v", s, err)
	}
	if strings.Join(p.calls, " ") != "fetch download" {
		t.Fatalf("must download but never install: %v", p.calls)
	}
	if !DownloadRequested(co.Base, *s.Target) {
		t.Fatal("interactive download was not requested")
	}
	if ReadOperation(co.Base, s.Target) != nil {
		t.Fatal("finished work must not leave live progress")
	}
}

func TestDownloadNowRefusesChangedAndWithdrawnTargets(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100)}
	co, _ := newCoordinator(t, p)
	s := foundTarget(t, co)
	if _, err := co.DownloadNow(context.Background(), "0.1.0-dev.19", s.Target.SHA256); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
	p.calls = nil
	p.feed.Withdrawn = []string{s.Target.Version}
	if _, err := co.DownloadNow(context.Background(), s.Target.Version, s.Target.SHA256); !errors.Is(err, ErrTargetChanged) {
		t.Fatal(err)
	}
	if strings.Join(p.calls, " ") != "fetch" {
		t.Fatalf("withdrawn asset was downloaded: %v", p.calls)
	}
}

func TestDownloadNowJoinsBackgroundDownloadWithoutDuplicatingIt(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100)}
	co, _ := newCoordinator(t, p)
	s := foundTarget(t, co)
	unlock, err := lock(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	defer ClearOperation(co.Base)
	if err := WriteOperation(co.Base, Operation{Version: s.Target.Version, SHA256: s.Target.SHA256, Phase: "retrying", Received: 1, Total: s.Target.Size}); err != nil {
		t.Fatal(err)
	}
	p.calls = nil
	if _, err := co.DownloadNow(context.Background(), s.Target.Version, s.Target.SHA256); err != nil {
		t.Fatal(err)
	}
	if !DownloadRequested(co.Base, *s.Target) || len(p.calls) != 0 {
		t.Fatalf("did not attach to existing download: %v", p.calls)
	}
}

func TestProgressRequiresLiveLockAndMatchingTarget(t *testing.T) {
	base := t.TempDir()
	target := &Target{Version: "0.1.0-dev.49", SHA256: strings.Repeat("a", 64), Size: 100}
	unlock, err := lock(base)
	if err != nil {
		t.Fatal(err)
	}
	defer ClearOperation(base)
	if err := WriteOperation(base, Operation{Version: target.Version, SHA256: target.SHA256, Phase: "downloading", Received: 30, Total: 100}); err != nil {
		t.Fatal(err)
	}
	if op := ReadOperation(base, target); op == nil || op.Received != 30 {
		t.Fatal(op)
	}
	other := *target
	other.SHA256 = strings.Repeat("b", 64)
	if ReadOperation(base, &other) != nil {
		t.Fatal("old asset progress must not appear for a new target")
	}
	unlock()
	if ReadOperation(base, target) != nil {
		t.Fatal("orphan progress must not appear live")
	}
	if err := os.WriteFile(filepath.Join(base, "update", "coordinator.lock"), []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}
	if ReadOperation(base, target) != nil {
		t.Fatal("dead owner progress must not appear live")
	}
}

func TestDiscoverNewTargetClearsPreviousReloginHint(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100)}
	co, _ := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.block(time.Now(), "needs_relogin", "已安裝，重新登入後生效")
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	s = foundTarget(t, co)
	if s.Stage != StageFound || s.Blocked != nil {
		t.Fatalf("%+v", s)
	}
}
