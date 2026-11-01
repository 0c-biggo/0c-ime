package autoupdate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Windows（#253）：外殼裝好就算數，不等重新登入；要核對的只有收據是這一版、新引擎接手了。
type windowsShellPlatform struct {
	*fakePlatform
	receiptErr error
}

func (windowsShellPlatform) ShellActiveWithoutRelogin() bool { return true }
func (p windowsShellPlatform) ShellReady(_ context.Context, version string) (bool, error) {
	return p.installed == version, p.receiptErr
}

// macOS：整包換 App、等登入，有自己的 #77／#98 路徑（ShellIdleGate＋ShellLoadProver），不交接。
type macShellPlatform struct{ provingShellPlatform }

func (macShellPlatform) ShellNeedsIdle() bool { return true }

func idleWindows(installed, running string) (*fakePlatform, Conditions) {
	cond := Conditions{Idle: 10 * time.Minute, IdleKnown: true}
	return &fakePlatform{installed: installed, running: running, startVer: installed, cond: cond}, cond
}

// stuckRelogin 模擬 0.2.7 的穩定更新器留下的狀態：按「立即安裝」裝好後一直停在 pending／needs_relogin。
// 最近剛查過索引，這次喚起不再查詢。
func stuckRelogin(t *testing.T, co *Coordinator, mode, detail string) {
	t.Helper()
	s, err := Load(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	now := co.Now()
	s.Mode = mode
	s.Stage = StagePending
	s.Target = &Target{Version: "0.2.8", Kind: "shell", SHA256: strings.Repeat("a", 64)}
	s.Blocked = &Blocked{Reason: "needs_relogin", Detail: detail, Since: now.Add(-48 * time.Hour)}
	s.LastCheck, s.NextCheck = now, now.Add(6*time.Hour)
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
}

func wantDone(t *testing.T, s *State, version string) {
	t.Helper()
	if s.Stage != StageActive || s.Target != nil || s.Blocked != nil {
		t.Fatalf("應該已完成：stage=%s target=%+v blocked=%+v", s.Stage, s.Target, s.Blocked)
	}
	if n := len(s.History); n == 0 || s.History[n-1].Kind != "active" || s.History[n-1].Version != version {
		t.Fatalf("歷程沒有記下生效：%+v", s.History)
	}
}

// 新引擎沒接手就不能報完成，也不能叫人登出。
func wantWaiting(t *testing.T, s *State, reason, detail string) {
	t.Helper()
	if s.Stage != StagePending || s.Target == nil || s.Blocked == nil || s.Blocked.Reason != reason || s.Blocked.Detail != detail {
		t.Fatalf("應該照實停在待完成（%s／%s）：stage=%s target=%+v blocked=%+v", reason, detail, s.Stage, s.Target, s.Blocked)
	}
	for _, e := range s.History {
		if e.Kind == "active" && e.Version == s.Target.Version {
			t.Fatalf("舊引擎還在跑卻報了完成：%+v", s.History)
		}
	}
	for _, word := range []string{"登出再登入", "重新登入"} {
		if strings.Contains(s.Blocked.Detail, word) {
			t.Fatalf("Windows 不該叫人%s：%q", word, s.Blocked.Detail)
		}
	}
}

func events(s *State, kind string) int {
	n := 0
	for _, e := range s.History {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// #253 的主要情境：按「立即安裝」裝好、引擎當場交接成新版，當場完成。不等登入、不發「請登出」的通知。
func TestWindowsShellInstallCompletesWithoutRelogin(t *testing.T) {
	p, _ := idleWindows("0.2.7", "0.2.7")
	p.feed = feedFor("0.2.8", "shell", 100)
	p.installHook = func() { p.installed, p.running = "0.2.8", "0.2.8" }
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	co.Consent = true
	s := run(t, co)
	wantDone(t, s, "0.2.8")
	if len(p.notices) != 0 {
		t.Fatalf("不該有通知：%v", p.notices)
	}
	if events(s, "installed") != 1 {
		t.Fatalf("歷程要記下這次是完整安裝（通知依此提醒重開程式）：%+v", s.History)
	}
}

// 安裝程式剛請舊引擎交接、登入工作還沒把新引擎叫起來：協調器自己叫起來核對版本，不看到沒有引擎就算完成。
func TestWindowsShellInstallStartsAndVerifiesTheNewEngine(t *testing.T) {
	p, _ := idleWindows("0.2.7", "0.2.7")
	p.feed = feedFor("0.2.8", "shell", 100)
	p.installHook = func() { p.installed, p.running = "0.2.8", "" }
	p.startVer = "0.2.8"
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	co.Consent = true
	s := run(t, co)
	wantDone(t, s, "0.2.8")
	if got := strings.Join(p.calls, " "); !strings.HasSuffix(got, "shell start") {
		t.Fatalf("calls %q", got)
	}
}

// 反例：組字中安裝，舊引擎沒交接（安裝程式已經等過 40 秒，不再等一輪）。不報完成、不叫人登出、不發通知；
// 下一次喚起（閒置）換好才完成。
func TestWindowsShellInstallWhileComposingWaitsHonestly(t *testing.T) {
	p, _ := idleWindows("0.2.7", "0.2.7")
	p.feed = feedFor("0.2.8", "shell", 100)
	p.installHook = func() { p.installed = "0.2.8" }
	p.startVer = "0.2.8"
	p.drainErr = errors.New("仍在組字")
	co, clk := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	co.Consent = true
	s := run(t, co)
	wantWaiting(t, s, "engine_busy", ShellEngineBusyDetail)
	for _, c := range p.calls {
		if c == "drain" {
			t.Fatalf("安裝程式剛等過交接，協調器不再等一輪：%v", p.calls)
		}
	}
	co.Consent = false
	// 一直在組字：照實停著，超過一天也不發通知（不用使用者處理）。
	clk.add(48 * time.Hour)
	p.calls = nil
	s = run(t, co)
	wantWaiting(t, s, "engine_busy", ShellEngineBusyDetail)
	if got := strings.Join(p.calls, " "); !strings.Contains(got, "drain") || strings.Contains(got, "start") || p.running != "0.2.7" {
		t.Fatalf("calls %q running %q", got, p.running)
	}
	if len(p.notices) != 0 {
		t.Fatalf("不該有通知：%v", p.notices)
	}
	// 組字結束：閒置時換好，當場完成。
	p.drainErr = nil
	clk.add(time.Hour)
	s = run(t, co)
	wantDone(t, s, "0.2.8")
	if p.running != "0.2.8" || events(s, "engine_handed_over") != 1 {
		t.Fatalf("running %q history %+v", p.running, s.History)
	}
}

// 卡在舊狀態的 0.2.7 使用者：新版的穩定更新器下一次喚起就清掉，不用登出。手動模式與暫停中也做，這是完成已裝好的更新。
func TestWindowsStuckReloginClearsWithoutSigningOut(t *testing.T) {
	for _, mode := range []string{ModeAuto, ModeAsk, ModeManual} {
		for _, detail := range []string{"已安裝，重新登入後生效", "重新登入後生效", "重新登入後生效；無法確認登入工作階段：x"} {
			t.Run(mode+"/"+detail, func(t *testing.T) {
				p, _ := idleWindows("0.2.8", "0.2.8")
				co, _ := newCoordinator(t, p)
				co.Platform = windowsShellPlatform{fakePlatform: p}
				stuckRelogin(t, co, mode, detail)
				s := run(t, co)
				wantDone(t, s, "0.2.8")
				if len(p.calls) != 0 || len(p.notices) != 0 {
					t.Fatalf("calls %v notices %v", p.calls, p.notices)
				}
			})
		}
	}
	t.Run("paused", func(t *testing.T) {
		p, _ := idleWindows("0.2.8", "0.2.8")
		co, _ := newCoordinator(t, p)
		co.Platform = windowsShellPlatform{fakePlatform: p}
		stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
		if _, err := EditState(co.Base, func(s *State) error { s.PausedUntil = co.Now().Add(time.Hour); return nil }); err != nil {
			t.Fatal(err)
		}
		wantDone(t, run(t, co), "0.2.8")
	})
}

// 使用者在設定程式按「檢查更新」也清得掉；只核對，不交接引擎。
func TestWindowsStuckReloginClearsOnCheck(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.8")
	p.feed = feedFor("0.2.8", "shell", 100)
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
	s, err := co.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantDone(t, s, "0.2.8")

	p, _ = idleWindows("0.2.8", "0.2.7")
	p.feed = feedFor("0.2.8", "shell", 100)
	co, _ = newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
	if s, err = co.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantWaiting(t, s, "waiting_idle", ShellEngineIdleDetail)
	for _, c := range p.calls {
		if c == "drain" || c == "start" {
			t.Fatalf("檢查更新不交接引擎：%v", p.calls)
		}
	}
}

// 登入工作與每日修復用的 ConfirmShell：協調器忙碌時回 ErrUpdateInProgress 讓呼叫端再等，不動狀態。
func TestConfirmShellClearsStuckStateAndWaitsForTheLock(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.8")
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	stuckRelogin(t, co, ModeManual, "已安裝，重新登入後生效")
	unlock, err := lock(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = co.ConfirmShell(context.Background()); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("鎖被占用時：%v", err)
	}
	unlock()
	if s, _ := Load(co.Base); s.Stage != StagePending {
		t.Fatalf("沒拿到鎖不該改狀態：%+v", s)
	}
	s, err := co.ConfirmShell(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantDone(t, s, "0.2.8")
	if len(p.calls) != 0 {
		t.Fatalf("只核對本機：%v", p.calls)
	}
}

// 卡住的使用者舊引擎還在：閒置時交接（手動模式也做）；還在打字就照實等，不改回要登出。
func TestWindowsStuckReloginWithOldEngine(t *testing.T) {
	for _, mode := range []string{ModeAuto, ModeManual} {
		t.Run(mode, func(t *testing.T) {
			p, cond := idleWindows("0.2.8", "0.2.7")
			p.cond.Idle = time.Minute
			co, clk := newCoordinator(t, p)
			co.Platform = windowsShellPlatform{fakePlatform: p}
			stuckRelogin(t, co, mode, "已安裝，重新登入後生效")
			s := run(t, co)
			wantWaiting(t, s, "waiting_idle", ShellEngineIdleDetail)
			if len(p.calls) != 0 || len(p.notices) != 0 {
				t.Fatalf("使用中不交接、也不提醒：%v %v", p.calls, p.notices)
			}
			p.cond = cond
			clk.add(15 * time.Minute)
			s = run(t, co)
			wantDone(t, s, "0.2.8")
			if got := strings.Join(p.calls, " "); got != "drain start" || p.running != "0.2.8" {
				t.Fatalf("calls %q running %q", got, p.running)
			}
		})
	}
}

// 使用者親自按「現在完成」：不等閒置，立刻交接。
func TestWindowsFinishHandsOverPendingEngineRightAway(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.7")
	p.cond.Idle = time.Second
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
	co.Consent = true
	wantDone(t, run(t, co), "0.2.8")
}

// 反例：新引擎叫不起來、或起來的不是這一版，都不報完成、不回復外殼、不把版本列入失敗；照實停著，下一次再試。
func TestWindowsHandoverFailureDoesNotReportDone(t *testing.T) {
	for name, startVer := range map[string]string{"wrong version": "0.2.7", "still not running": "none"} {
		t.Run(name, func(t *testing.T) {
			p, _ := idleWindows("0.2.8", "0.2.7")
			p.feed = feedFor("0.2.8", "shell", 100)
			p.startVer = startVer
			if startVer == "none" {
				p.running = ""
				p.startVer = ""
			}
			co, clk := newCoordinator(t, p)
			co.Platform = windowsShellPlatform{fakePlatform: p}
			stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
			s := run(t, co)
			wantWaiting(t, s, "engine_handover_failed", ShellEngineFailedDetail)
			if len(s.Failed) != 0 || events(s, "engine_handover_failed") != 1 {
				t.Fatalf("failed %v history %+v", s.Failed, s.History)
			}
			clk.add(48 * time.Hour)
			p.startVer = "0.2.8"
			wantDone(t, run(t, co), "0.2.8")
		})
	}
}

// 收據已不是目標版本（裝好後又回復）：這個待辦不會再完成，清掉，不留在「已安裝」；下一次查詢照常再提供新版。
func TestWindowsRollbackWhilePendingDropsTheTarget(t *testing.T) {
	p, _ := idleWindows("0.2.7", "0.2.7")
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
	s := run(t, co)
	if s.Stage != StageIdle || s.Target != nil || s.Blocked != nil {
		t.Fatalf("%+v", s)
	}
	if events(s, "active") != 0 {
		t.Fatalf("沒裝好的版本不該算生效：%+v", s.History)
	}
}

// 收據讀不到：不知道裝了什麼，不報完成也不丟掉待辦。
func TestWindowsUnreadableReceiptKeepsTheTarget(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.8")
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p, receiptErr: errors.New("讀不到收據")}
	stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
	s := run(t, co)
	if s.Stage != StagePending || s.Target == nil || s.Blocked == nil || s.Blocked.Reason != "install_unknown" {
		t.Fatalf("%+v", s)
	}
}

// pending 不再擋核心更新：舊引擎還沒接手時來了核心更新，由核心更新自己的交接帶上新引擎。較新的外殼一樣可以取代。
func TestWindowsPendingShellDoesNotBlockCoreUpdate(t *testing.T) {
	for _, kind := range []string{"core", "shell"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := idleWindows("0.2.8", "0.2.7")
			p.cond.Idle = time.Minute
			p.feed = feedFor("0.2.9", kind, 100)
			p.feed.Serial = 2
			p.shellErr = ErrNeedsConsent
			co, _ := newCoordinator(t, p)
			co.Platform = windowsShellPlatform{fakePlatform: p}
			co.Force = true
			stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
			s := run(t, co)
			if s.Target == nil || s.Target.Version != "0.2.9" || s.Target.Kind != kind {
				t.Fatalf("target %+v calls %v", s.Target, p.calls)
			}
			if s.Blocked != nil && strings.Contains(s.Blocked.Detail, "已安裝") {
				t.Fatalf("上一版的受阻說明不該留在新目標上：%+v", s.Blocked)
			}
		})
	}
}

// 精靈、一行安裝或回復時正在組字，舊引擎沒交接：沒有待辦，排程閒置時也要換掉，否則舊引擎會一直留到登出。
func TestWindowsStaleEngineWithoutTargetIsHandedOverWhenIdle(t *testing.T) {
	for _, mode := range []string{ModeAuto, ModeManual} {
		t.Run(mode, func(t *testing.T) {
			p, cond := idleWindows("0.2.8", "0.2.7")
			p.cond.Idle = time.Minute
			co, clk := newCoordinator(t, p)
			co.Platform = windowsShellPlatform{fakePlatform: p}
			if _, err := EditState(co.Base, func(s *State) error {
				s.Mode, s.LastCheck, s.NextCheck = mode, co.Now(), co.Now().Add(6*time.Hour)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			run(t, co)
			if len(p.calls) != 0 {
				t.Fatalf("使用中不換：%v", p.calls)
			}
			p.cond = cond
			p.drainErr = errors.New("仍在組字")
			clk.add(15 * time.Minute)
			if s := run(t, co); p.running != "0.2.7" || events(s, "engine_handover_waiting") != 1 {
				t.Fatalf("組字中不換：running %q history %+v", p.running, s.History)
			}
			p.drainErr, p.calls = nil, nil
			clk.add(15 * time.Minute)
			s := run(t, co)
			if got := strings.Join(p.calls, " "); got != "drain start" || p.running != "0.2.8" || events(s, "engine_handed_over") != 1 {
				t.Fatalf("calls %q running %q history %+v", got, p.running, s.History)
			}
			p.calls = nil
			clk.add(15 * time.Minute)
			if run(t, co); len(p.calls) != 0 {
				t.Fatalf("換好之後不再動：%v", p.calls)
			}
		})
	}
}

// 下載好、等確認的新版不受影響：受阻原因與起算時間不變，也不被交接蓋掉。
func TestStaleEngineHandoverKeepsConsentWaiting(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.7")
	p.feed = feedFor("0.2.9", "shell", 100)
	p.shellErr = ErrNeedsConsent
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	s := run(t, co)
	if s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "needs_consent" || p.running != "0.2.8" {
		t.Fatalf("stage %s blocked %+v running %q", s.Stage, s.Blocked, p.running)
	}
}

// 切換中的核心更新與等重新啟動的引擎有自己的收尾，這裡不插手。
func TestStaleEngineHandoverLeavesCoreTransactionsAlone(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.7")
	co, _ := newCoordinator(t, p)
	co.Platform = windowsShellPlatform{fakePlatform: p}
	s, _ := Load(co.Base)
	s.RestartPending = true
	co.defaults()
	co.handOverStaleEngine(context.Background(), s, co.Now())
	if len(p.calls) != 0 {
		t.Fatalf("calls %v", p.calls)
	}
}

// macOS 與其他等登入的平台：行為不變，舊引擎照舊留著，仍要重新登入。
func TestWaitingForLoginStaysOnOtherPlatforms(t *testing.T) {
	for name, wrap := range map[string]func(*fakePlatform) Platform{
		"macos": func(p *fakePlatform) Platform {
			return macShellPlatform{provingShellPlatform{&loginShellPlatform{fakePlatform: p}}}
		},
		"login-shell": func(p *fakePlatform) Platform { return &loginShellPlatform{fakePlatform: p} },
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := idleWindows("0.2.8", "0.2.7")
			co, _ := newCoordinator(t, p)
			co.Platform = wrap(p)
			stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
			s := run(t, co)
			if len(p.calls) != 0 || p.running != "0.2.7" {
				t.Fatalf("calls %v running %q", p.calls, p.running)
			}
			if s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" {
				t.Fatalf("%+v", s)
			}
			// 沒有待辦時也不交接舊引擎（macOS 有 #170 自己的交接）。
			s.Target, s.Stage, s.Blocked = nil, StageIdle, nil
			co.defaults()
			co.handOverStaleEngine(context.Background(), s, co.Now())
			if len(p.calls) != 0 {
				t.Fatalf("calls %v", p.calls)
			}
		})
	}
}

// Linux 在登入中重載外殼：即使平台也宣稱不必登入，等待中的外殼仍走原本的安裝路徑，不在這裡動引擎。
func TestPendingShellEngineHandoverSkipsLiveShell(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.7")
	co, _ := newCoordinator(t, p)
	co.Platform = struct {
		*liveShellFake
		ShellWithoutRelogin
	}{&liveShellFake{p, co.Base, t}, windowsShellPlatform{}}
	stuckRelogin(t, co, ModeAuto, "已安裝，重新登入後生效")
	s, _ := Load(co.Base)
	co.defaults()
	co.handOverPendingShellEngine(context.Background(), s, co.Now(), true)
	if len(p.calls) != 0 || p.running != "0.2.7" || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" {
		t.Fatalf("calls %v running %q blocked %+v", p.calls, p.running, s.Blocked)
	}
}

// 沒有待辦、舊引擎也沒換成（選單那一列按下去之後）：說得出原因，通知才不會寫「已是最新」。原因不寫進狀態檔，每次喚起重新判斷。
type startFailsPlatform struct{ windowsShellPlatform }

func (startFailsPlatform) StartEngine(context.Context) error { return errors.New("起不來") }

func TestStaleEngineLeftBehindSaysWhy(t *testing.T) {
	lowBattery := Conditions{BatteryKnown: true, Battery: 10, Idle: 10 * time.Minute, IdleKnown: true}
	for _, c := range []struct {
		name    string
		consent bool
		set     func(p *fakePlatform)
		wrap    func(windowsShellPlatform) Platform
		want    string
	}{
		{name: "組字中", consent: true, set: func(p *fakePlatform) { p.drainErr = errors.New("組字中的 session：1") }, want: ShellEngineBusyDetail},
		{name: "電量低", consent: true, set: func(p *fakePlatform) { p.cond = lowBattery }, want: ShellEngineFailedDetail},
		{name: "檢查更新時還在使用", set: func(p *fakePlatform) { p.cond.Idle = time.Minute }, want: ShellEngineIdleDetail},
		{name: "新引擎起不來", consent: true, wrap: func(p windowsShellPlatform) Platform { return startFailsPlatform{p} }, want: ShellEngineFailedDetail},
		{name: "版本不符", consent: true, set: func(p *fakePlatform) { p.startVer = "0.2.7" }, want: ShellEngineFailedDetail},
		{name: "換好了", consent: true},
		{name: "引擎本來就是這一版", consent: true, set: func(p *fakePlatform) { p.running = "0.2.8" }},
		{name: "引擎沒在跑", consent: true, set: func(p *fakePlatform) { p.running = "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _ := idleWindows("0.2.8", "0.2.7")
			if c.set != nil {
				c.set(p)
			}
			co, _ := newCoordinator(t, p)
			win := windowsShellPlatform{fakePlatform: p}
			co.Platform = win
			if c.wrap != nil {
				co.Platform = c.wrap(win)
			}
			co.Consent = c.consent
			if _, err := EditState(co.Base, func(s *State) error {
				s.Mode, s.LastCheck, s.NextCheck = ModeManual, co.Now(), co.Now().Add(6*time.Hour)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			s := run(t, co)
			if s.Target != nil || s.StaleEngine != c.want {
				t.Fatalf("target %+v StaleEngine %q，要 %q", s.Target, s.StaleEngine, c.want)
			}
			stored, err := Load(co.Base)
			if err != nil {
				t.Fatal(err)
			}
			if stored.StaleEngine != "" {
				t.Fatalf("不寫進狀態檔：%q", stored.StaleEngine)
			}
		})
	}
}

// 等重新登入的平台（macOS）沒有這條路：通知文字不變。
func TestStaleEngineReasonIsWindowsOnly(t *testing.T) {
	p, _ := idleWindows("0.2.8", "0.2.7")
	co, _ := newCoordinator(t, p)
	co.Platform = macShellPlatform{provingShellPlatform{&loginShellPlatform{fakePlatform: p}}}
	co.Consent = true
	if _, err := EditState(co.Base, func(s *State) error {
		s.LastCheck, s.NextCheck = co.Now(), co.Now().Add(6*time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s := run(t, co); s.StaleEngine != "" {
		t.Fatalf("%q", s.StaleEngine)
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
