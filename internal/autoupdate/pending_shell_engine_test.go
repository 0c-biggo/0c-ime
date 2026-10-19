package autoupdate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Windows：外殼要重新登入才全面生效，引擎可以先換（PendingShellEngineHandover）。
type windowsShellPlatform struct{ *loginShellPlatform }

func (windowsShellPlatform) HandsOverEngineBeforeRelogin() bool { return true }

// macOS：整包換 App、等登入，有自己的 #77／#98 路徑（ShellIdleGate＋ShellLoadProver），不交接。
type macShellPlatform struct{ provingShellPlatform }

func (macShellPlatform) ShellNeedsIdle() bool { return true }

// pendingShellState 模擬舊的穩定更新器（例如 dev.64）背景裝好 0.2.0 外殼後留下的狀態：
// 外殼已安裝、等重新登入，舊引擎仍常駐。最近剛查過索引，這次喚起不再查詢。
func pendingShellState(t *testing.T, co *Coordinator, mode string) time.Time {
	t.Helper()
	s, err := Load(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	now := co.Now()
	s.Mode = mode
	s.Stage = StagePending
	s.Target = &Target{Version: "0.2.0", Kind: "shell", SHA256: strings.Repeat("a", 64)}
	s.Blocked = &Blocked{Reason: "needs_relogin", Detail: "已安裝，重新登入後生效", Since: now.Add(-time.Hour)}
	s.LastCheck, s.NextCheck = now, now.Add(6*time.Hour)
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	return s.Blocked.Since
}

func stillAwaitingRelogin(t *testing.T, s *State, since time.Time) {
	t.Helper()
	if s.Stage != StagePending || s.Target == nil || s.Target.Version != "0.2.0" || s.Blocked == nil ||
		s.Blocked.Reason != "needs_relogin" || !s.Blocked.Since.Equal(since) {
		t.Fatalf("外殼仍要重新登入才生效：stage=%s target=%+v blocked=%+v", s.Stage, s.Target, s.Blocked)
	}
}

// handoverEvent 是交接留下的歷程（之後可能還有提醒等其他紀錄）。
func handoverEvent(s *State) string {
	for i := len(s.History) - 1; i >= 0; i-- {
		if k := s.History[i].Kind; strings.HasPrefix(k, "engine_hand") {
			return k
		}
	}
	return ""
}

// 舊版穩定更新器背景裝好外殼、舊引擎常駐：新版下一次喚起（閒置）時交接引擎並核對版本，外殼仍等重新登入；
// 手動模式也做（完成已裝好的更新）。引擎換好之後不再重複交接。
func TestWindowsPendingShellHandsOverOldEngine(t *testing.T) {
	for _, mode := range []string{ModeAuto, ModeManual} {
		t.Run(mode, func(t *testing.T) {
			p := &fakePlatform{installed: "0.2.0", running: "0.1.0-dev.64", startVer: "0.2.0",
				cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}}
			co, clk := newCoordinator(t, p)
			co.Platform = windowsShellPlatform{&loginShellPlatform{fakePlatform: p}}
			since := pendingShellState(t, co, mode)
			s := run(t, co)
			if got := strings.Join(p.calls, " "); got != "drain start" || p.running != "0.2.0" {
				t.Fatalf("calls %q running %q", got, p.running)
			}
			stillAwaitingRelogin(t, s, since)
			if handoverEvent(s) != "engine_handed_over" {
				t.Fatalf("history %+v", s.History)
			}
			p.calls = nil
			clk.add(time.Hour)
			s = run(t, co)
			if len(p.calls) != 0 {
				t.Fatalf("引擎已是新版：不再交接：%v", p.calls)
			}
			stillAwaitingRelogin(t, s, since)
		})
	}
}

// 仍在組字（Drain 失敗）：不啟動、不換引擎，照舊等登出；使用中（不閒置）連交接都不試，受阻原因不改成 waiting_idle。
func TestWindowsPendingShellKeepsEngineWhileComposingOrActive(t *testing.T) {
	p := &fakePlatform{installed: "0.2.0", running: "0.1.0-dev.64", startVer: "0.2.0",
		cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}, drainErr: errors.New("仍在組字")}
	co, clk := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{&loginShellPlatform{fakePlatform: p}}
	since := pendingShellState(t, co, ModeAuto)
	s := run(t, co)
	if got := strings.Join(p.calls, " "); got != "drain" || p.running != "0.1.0-dev.64" {
		t.Fatalf("calls %q running %q", got, p.running)
	}
	stillAwaitingRelogin(t, s, since)
	if handoverEvent(s) != "engine_handover_waiting" {
		t.Fatalf("history %+v", s.History)
	}
	p.calls, p.drainErr, p.cond.Idle = nil, nil, time.Minute
	clk.add(time.Hour)
	s = run(t, co)
	if len(p.calls) != 0 {
		t.Fatalf("使用中不交接：%v", p.calls)
	}
	stillAwaitingRelogin(t, s, since)
}

// 新引擎回報的版本不對：只記錄，不回復外殼、不 activate，下一次喚起再試。
func TestWindowsPendingShellRecordsWrongEngineVersion(t *testing.T) {
	p := &fakePlatform{installed: "0.2.0", running: "0.1.0-dev.64", startVer: "0.1.0-dev.64",
		cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}}
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{&loginShellPlatform{fakePlatform: p}}
	since := pendingShellState(t, co, ModeAuto)
	s := run(t, co)
	if got := strings.Join(p.calls, " "); got != "drain start" {
		t.Fatalf("calls %q", got)
	}
	stillAwaitingRelogin(t, s, since)
	if handoverEvent(s) != "engine_handover_failed" || len(s.Failed) != 0 {
		t.Fatalf("history %+v failed %v", s.History, s.Failed)
	}
}

// macOS 與沒有實作 PendingShellEngineHandover 的等登入平台：舊引擎照舊留著，行為不變。
func TestPendingShellEngineHandoverOnlyOnWindows(t *testing.T) {
	for name, wrap := range map[string]func(*fakePlatform) Platform{
		"macos": func(p *fakePlatform) Platform {
			return macShellPlatform{provingShellPlatform{&loginShellPlatform{fakePlatform: p}}}
		},
		"login-shell": func(p *fakePlatform) Platform { return &loginShellPlatform{fakePlatform: p} },
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePlatform{installed: "0.2.0", running: "0.1.0-dev.64", startVer: "0.2.0",
				cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}}
			co, _ := newCoordinator(t, p)
			co.Platform = wrap(p)
			since := pendingShellState(t, co, ModeAuto)
			s := run(t, co)
			if len(p.calls) != 0 || p.running != "0.1.0-dev.64" {
				t.Fatalf("calls %v running %q", p.calls, p.running)
			}
			stillAwaitingRelogin(t, s, since)
		})
	}
}

// Linux 在登入中重載外殼：即使平台也宣稱可以交接，等待中的外殼仍走原本的安裝路徑，不在這裡動引擎。
func TestPendingShellEngineHandoverSkipsLiveShell(t *testing.T) {
	p := &fakePlatform{installed: "0.2.0", running: "0.1.0-dev.64", startVer: "0.2.0",
		cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}}
	co, _ := newCoordinator(t, p)
	co.Platform = struct {
		*liveShellFake
		PendingShellEngineHandover
	}{&liveShellFake{p, co.Base, t}, windowsShellPlatform{}}
	pendingShellState(t, co, ModeAuto)
	s, _ := Load(co.Base)
	co.defaults()
	co.handOverPendingShellEngine(context.Background(), s, co.Now())
	if len(p.calls) != 0 || p.running != "0.1.0-dev.64" || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" {
		t.Fatalf("calls %v running %q blocked %+v", p.calls, p.running, s.Blocked)
	}
}

// 每次喚起都失敗的交接只記一次，不擠掉歷程裡的安裝紀錄。
func TestRecordOnceSkipsRepeats(t *testing.T) {
	s := &State{}
	now := time.Unix(1, 0)
	for i := 0; i < 5; i++ {
		recordOnce(s, now, "engine_handover_failed", "0.2.0", "x")
	}
	recordOnce(s, now, "engine_handover_failed", "0.2.1", "x")
	if len(s.History) != 2 {
		t.Fatalf("%+v", s.History)
	}
}
