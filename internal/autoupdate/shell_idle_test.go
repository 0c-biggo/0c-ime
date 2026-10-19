package autoupdate

import (
	"strings"
	"testing"
	"time"
)

type idleGatedShell struct{ *fakePlatform }

func (*idleGatedShell) ShellNeedsIdle() bool { return true }

// macOS replaces the whole App but does not reload the current IMK shell. It
// still needs the same idle/battery gate before changing the installation.
func TestNonReloadableShellWaitsForIdleBeforeInstalling(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40",
		feed: feedFor("0.1.0-dev.41", "shell", 100), shellPend: true,
		cond: Conditions{Idle: time.Minute, IdleKnown: true}}
	co, clk := newCoordinator(t, p)
	co.Platform = &idleGatedShell{p}
	s := run(t, co)
	if s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "waiting_idle" || strings.Contains(strings.Join(p.calls, " "), "shell") {
		t.Fatalf("must not install a live App while user is active: stage=%s blocked=%+v calls=%v", s.Stage, s.Blocked, p.calls)
	}
	p.cond.Idle = 5 * time.Minute
	clk.add(5 * time.Minute)
	s = run(t, co)
	if s.Stage != StagePending || !strings.Contains(strings.Join(p.calls, " "), "shell") {
		t.Fatalf("idle shell must install and await actual runtime: stage=%s calls=%v", s.Stage, p.calls)
	}
}

func TestNonReloadableShellManualConsentDoesNotBypassLowBattery(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40",
		feed: feedFor("0.1.0-dev.41", "shell", 100), shellPend: true,
		cond: Conditions{IdleKnown: true, BatteryKnown: true, Battery: 35}}
	co, _ := newCoordinator(t, p)
	co.Platform = &idleGatedShell{p}
	co.Consent = true
	s := run(t, co)
	if s.Blocked == nil || s.Blocked.Reason != "low_battery" || strings.Contains(strings.Join(p.calls, " "), "shell") {
		t.Fatalf("low battery must block shell even with consent: %+v %v", s.Blocked, p.calls)
	}
	p.cond.Charging = true
	if s = run(t, co); s.Stage != StagePending {
		t.Fatalf("explicit consent may bypass idle after power is safe: %s", s.Stage)
	}
}
