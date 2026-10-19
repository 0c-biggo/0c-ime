package autoupdate

import (
	"os"
	"syscall"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/update"
)

func TestDiskSpaceFailureKeepsTargetAndRecoversWithoutFailedVersion(t *testing.T) {
	for _, failure := range []error{update.ErrInsufficientSpace, &os.PathError{Op: "write", Path: "artificial", Err: syscall.ENOSPC}} {
		p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "core", 100), downloadErr: failure, cond: Conditions{IdleKnown: true, Idle: time.Hour}}
		co, _ := newCoordinator(t, p)
		s := run(t, co)
		if s.Stage != StageFound || s.Target == nil || s.Blocked == nil || s.Blocked.Reason != "disk_full" || len(s.Failed) != 0 {
			t.Fatalf("disk full lost retryable target: %+v", s)
		}
		if p.pointer != "" {
			t.Fatal("disk full changed pointer")
		}
		p.downloadErr = nil
		if s = run(t, co); s.Stage != StageActive || s.Blocked != nil || len(s.Failed) != 0 {
			t.Fatalf("space recovery did not complete: %+v", s)
		}
	}
}

func TestShellDiskSpaceFailureRetainsPreparedPackage(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), shellErr: syscall.ENOSPC, shellPend: true}
	co, _ := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StagePrepared || s.Download == "" || s.Blocked == nil || s.Blocked.Reason != "disk_full" {
		t.Fatal("shell disk full lost prepared package", s)
	}
	p.shellErr = nil
	if s = run(t, co); s.Stage != StagePending || s.Blocked.Reason != "needs_relogin" {
		t.Fatal("shell package did not recover", s)
	}
}
