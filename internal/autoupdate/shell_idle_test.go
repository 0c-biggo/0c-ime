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

// archiveGatedShell：等不等閒置看已下載的套件（Windows 註冊不變的外殼更新，#294）。
type archiveGatedShell struct {
	*fakePlatform
	idle     bool
	archives []string
}

func (a *archiveGatedShell) ShellNeedsIdleFor(archive string) bool {
	a.archives = append(a.archives, archive)
	return a.idle
}

func TestShellArchiveGateWaitsForIdleOnlyWhenThePlatformSaysSo(t *testing.T) {
	// 能在背景裝好的外殼更新：使用中先等閒置，閒置後才安裝；閘門拿到的是已下載的套件。
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40",
		feed: feedFor("0.1.0-dev.41", "shell", 100), shellPend: true,
		cond: Conditions{Idle: time.Minute, IdleKnown: true}}
	co, clk := newCoordinator(t, p)
	gate := &archiveGatedShell{fakePlatform: p, idle: true}
	co.Platform = gate
	s := run(t, co)
	if s.Blocked == nil || s.Blocked.Reason != "waiting_idle" || strings.Contains(strings.Join(p.calls, " "), "shell") {
		t.Fatalf("unattended shell install must wait for idle: blocked=%+v calls=%v", s.Blocked, p.calls)
	}
	if len(gate.archives) == 0 || gate.archives[0] == "" || gate.archives[0] != s.Download {
		t.Fatalf("gate must see the downloaded archive: %v (download %q)", gate.archives, s.Download)
	}
	p.cond.Idle = 5 * time.Minute
	clk.add(5 * time.Minute)
	if s = run(t, co); !strings.Contains(strings.Join(p.calls, " "), "shell") {
		t.Fatalf("idle shell must install: stage=%s calls=%v", s.Stage, p.calls)
	}

	// 要使用者確認的（Windows 要改註冊）：不等閒置，馬上交給平台，停在 needs_consent。
	q := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40",
		feed: feedFor("0.1.0-dev.41", "shell", 100), shellErr: ErrNeedsConsent,
		cond: Conditions{Idle: time.Minute, IdleKnown: true}}
	co, _ = newCoordinator(t, q)
	co.Platform = &archiveGatedShell{fakePlatform: q}
	if s = run(t, co); s.Blocked == nil || s.Blocked.Reason != "needs_consent" || !strings.Contains(strings.Join(q.calls, " "), "shell") {
		t.Fatalf("a shell update that needs consent must not wait for idle: %+v calls=%v", s.Blocked, q.calls)
	}
}
