package autoupdate

import (
	"errors"
	"testing"
	"time"
)

type failingRevert struct {
	*fakePlatform
	failure error
}

func (p *failingRevert) RevertCore(dir string) error {
	if p.failure != nil {
		return p.failure
	}
	return p.fakePlatform.RevertCore(dir)
}

func TestWithdrawnSwitchedTargetSurvivesRollbackFailure(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.41", feed: feedFor("0.1.0-dev.41", "core", 100, "0.1.0-dev.41"), pointer: "versions/0.1.0-dev.41-x", prevPointer: "versions/0.1.0-dev.40-x"}
	co, _ := newCoordinator(t, p)
	broken := &failingRevert{fakePlatform: p, failure: errors.New("artificial pointer write failure")}
	co.Platform = broken
	s, _ := Load(co.Base)
	s.Target = &Target{Version: "0.1.0-dev.41", Kind: "core"}
	s.Prepared = p.pointer
	s.Switched = true
	s.Stage = StagePrepared
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	s = run(t, co)
	if s.Target == nil || !s.Switched || s.Stage != StageFailed || !s.failed("0.1.0-dev.41") || s.Blocked.Reason != "revert_failed" || p.pointer != "versions/0.1.0-dev.41-x" {
		t.Fatalf("failed withdrawal forgot transaction: %+v pointer=%s", s, p.pointer)
	}
	broken.failure = nil
	s = run(t, co)
	if s.Switched || s.RestartPending || !s.failed("0.1.0-dev.41") || p.pointer != "versions/0.1.0-dev.40-x" || p.running != "0.1.0-dev.40" {
		t.Fatalf("retry did not restore verified engine: %+v pointer=%s running=%s", s, p.pointer, p.running)
	}
	co.Force = true
	if s = run(t, co); s.Target != nil || s.Stage != StageIdle {
		t.Fatal("next verified withdrawal query must clear obsolete target", s)
	}
}

func TestNewReleaseDoesNotResetLongIdleWaitReminder(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "core", 100), cond: Conditions{IdleKnown: true, Idle: time.Minute}}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	since := s.Blocked.Since
	clk.add(23 * time.Hour)
	p.feed = feedFor("0.1.0-dev.42", "core", 100)
	run(t, co)
	clk.add(2 * time.Hour)
	p.feed = feedFor("0.1.0-dev.43", "core", 100)
	co.Force = true
	s = run(t, co)
	if !s.Blocked.Since.Equal(since) || len(p.notices) != 1 || s.Target.Version != "0.1.0-dev.43" {
		t.Fatalf("new releases reset obstruction age: %+v notices=%v", s.Blocked, p.notices)
	}
}
