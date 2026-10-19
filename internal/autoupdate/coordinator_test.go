package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/update"
)

type fakePlatform struct {
	installed   string
	running     string // 運作中的引擎版本；空字串為未執行
	feed        *update.SignedFeed
	fetchErr    error
	downloadErr error
	cond        Conditions
	drainErr    error
	startVer    string // StartEngine 後運作的版本；空字串為目標版本
	shellErr    error
	shellPend   bool
	pointer     string
	prevPointer string
	notices     []string
	actions     []string
	calls       []string
	installHook func() // InstallShell 成功時改變平台狀態（例如安裝器交接了引擎）
}

func (f *fakePlatform) Installed() (string, error) { return f.installed, nil }
func (f *fakePlatform) Running(context.Context) (string, bool, error) {
	return f.running, f.running != "", nil
}
func (f *fakePlatform) Fetch(context.Context, uint64) (*update.SignedFeed, error) {
	f.calls = append(f.calls, "fetch")
	return f.feed, f.fetchErr
}
func (f *fakePlatform) Download(_ context.Context, t Target, _ string) (string, error) {
	f.calls = append(f.calls, "download")
	return "/tmp/" + t.Version + ".zip", f.downloadErr
}
func (f *fakePlatform) Prepare(_ context.Context, _ string, t Target) (string, error) {
	f.calls = append(f.calls, "prepare")
	return "versions/" + t.Version + "-x", nil
}
func (f *fakePlatform) SwitchCore(dir, version string) error {
	if f.pointer != dir {
		f.prevPointer, f.pointer = f.pointer, dir
	}
	f.calls = append(f.calls, "switch")
	return nil
}
func (f *fakePlatform) RevertCore(dir string) error {
	if f.pointer == dir {
		f.pointer = f.prevPointer
	}
	f.calls = append(f.calls, "revert")
	return nil
}
func (f *fakePlatform) Drain(context.Context) error {
	f.calls = append(f.calls, "drain")
	if f.drainErr == nil {
		f.running = ""
	}
	return f.drainErr
}
func (f *fakePlatform) StartEngine(context.Context) error {
	f.calls = append(f.calls, "start")
	f.running = f.startVer
	if f.running == "" && f.pointer != "" {
		f.running = strings.TrimSuffix(strings.TrimPrefix(f.pointer, "versions/"), "-x")
	}
	return nil
}
func (f *fakePlatform) InstallShell(context.Context, string) (bool, error) {
	f.calls = append(f.calls, "shell")
	if f.shellErr == nil && f.installHook != nil {
		f.installHook()
	}
	return f.shellPend, f.shellErr
}
func (f *fakePlatform) Conditions() Conditions { return f.cond }
func (f *fakePlatform) Notify(n Notice) error {
	f.notices = append(f.notices, n.Title)
	f.actions = append(f.actions, n.Action)
	return nil
}
func (f *fakePlatform) Target() (string, string) { return "linux", "amd64" }

func feedFor(version, kind string, rollout int, withdrawn ...string) *update.SignedFeed {
	return &update.SignedFeed{Serial: 1, Version: version, Rollout: rollout, Withdrawn: withdrawn,
		Assets: []update.SignedAsset{{Asset: update.Asset{OS: "linux", Arch: "amd64", URL: "https://x/" + version, SHA256: strings.Repeat("a", 64), Size: 1}, Kind: kind}}}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newCoordinator(t *testing.T, p *fakePlatform) (*Coordinator, *clock) {
	c := &clock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	return &Coordinator{Base: t.TempDir(), Platform: p, Now: c.now, Jitter: func(time.Duration) time.Duration { return 0 }}, c
}

func run(t *testing.T, co *Coordinator) *State {
	t.Helper()
	s, err := co.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// E01 的策略面：安裝 A 後發布 B，閒置時自動發現、下載、準備、切換並核對生效，不需要使用者操作。
func TestCoreUpdateCompletesWithoutUserActionWhenIdle(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}}
	co, _ := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StageActive || len(p.notices) != 0 {
		t.Fatalf("stage %s notices %v calls %v", s.Stage, p.notices, p.calls)
	}
	want := "fetch download prepare switch drain start"
	if strings.Join(p.calls, " ") != want {
		t.Fatalf("calls %v", p.calls)
	}
}

func TestWaitsForIdleAndRemindsOnlyAfter24Hours(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Minute, IdleKnown: true}}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "waiting_idle" || len(p.notices) != 0 {
		t.Fatalf("%+v %v", s.Blocked, p.notices)
	}
	since := s.Blocked.Since
	clk.add(23 * time.Hour)
	if s = run(t, co); len(p.notices) != 0 || !s.Blocked.Since.Equal(since) {
		t.Fatal("must not remind before 24h or reset the blocked clock")
	}
	clk.add(2 * time.Hour)
	if s = run(t, co); len(p.notices) != 1 {
		t.Fatalf("expected one reminder after 24h: %v", p.notices)
	}
	clk.add(time.Hour)
	if run(t, co); len(p.notices) != 1 {
		t.Fatal("at most one reminder per day")
	}
	p.cond.Idle = 20 * time.Minute
	clk.add(time.Hour)
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("idle again must complete: %s", s.Stage)
	}
}

func TestHealthFailureRevertsAndSuspendsVersion(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}, startVer: "0.1.0-dev.17"}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if want := "fetch download prepare switch drain start revert drain start"; strings.Join(p.calls, " ") != want {
		t.Fatalf("the unhealthy engine must be drained before the previous one starts: %v", p.calls)
	}
	if s.Stage != StageFailed || p.pointer != "" || !s.failed("0.1.0-dev.18") || len(p.notices) != 1 {
		t.Fatalf("stage %s pointer %q failed %v notices %v", s.Stage, p.pointer, s.Failed, p.notices)
	}
	clk.add(7 * time.Hour)
	p.calls = nil
	s = run(t, co)
	for _, c := range p.calls {
		if c == "download" || c == "switch" {
			t.Fatal("a failed version must not be retried automatically")
		}
	}
}

func TestShellUpdateNeedsConsentAndDoesNotResetBlockedClockForNewerVersion(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellErr: ErrNeedsConsent}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.Blocked == nil || s.Blocked.Reason != "needs_consent" || len(p.notices) != 1 {
		t.Fatalf("%+v %v", s.Blocked, p.notices)
	}
	since := s.Blocked.Since
	clk.add(7 * time.Hour)
	p.feed = feedFor("0.1.0-dev.19", "shell", 100)
	p.feed.Serial = 2
	s = run(t, co)
	if s.Target.Version != "0.1.0-dev.19" || !s.Blocked.Since.Equal(since) {
		t.Fatal("a newer version must replace the target without resetting the blocked clock")
	}
}

func TestShellUpdatePendingUntilRunningVersionMatches(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StagePending || s.Blocked.Reason != "needs_relogin" {
		t.Fatalf("%s %+v", s.Stage, s.Blocked)
	}
	p.running = "0.1.0-dev.18"
	clk.add(time.Hour)
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("running version matched but stage %s", s.Stage)
	}
}

func TestShellReceiptDoesNotDiscardPendingActivationOnScheduledCheck(t *testing.T) {
	for _, running := range []string{"0.1.0-dev.17", ""} {
		t.Run("running="+running, func(t *testing.T) {
			p := &fakePlatform{installed: "0.1.0-dev.17", running: running, feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true}
			co, clk := newCoordinator(t, p)
			s := run(t, co)
			if s.Stage != StagePending || s.Blocked == nil {
				t.Fatalf("expected pending shell: %+v", s)
			}
			since := s.Blocked.Since
			// Windows 安裝已寫新版收據，但尚未重新登入，運作中的引擎仍舊版或未啟動。
			p.installed = p.feed.Version
			clk.add(7 * time.Hour)
			p.calls = nil
			s = run(t, co)
			if s.Target == nil || s.Target.Version != p.installed || s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" || !s.Blocked.Since.Equal(since) {
				t.Fatalf("scheduled check discarded pending activation: %+v", s)
			}
			if strings.Join(p.calls, " ") != "fetch" {
				t.Fatalf("must check without reinstalling: %v", p.calls)
			}
			p.running = p.installed
			clk.add(7 * time.Hour)
			s = run(t, co)
			if s.Stage != StageActive || s.Target != nil || s.Blocked != nil {
				t.Fatalf("new runtime must complete confirmation after checking feed: %+v", s)
			}
		})
	}
}

func TestDiscoveryRulesWithdrawnRolloutBackoffAndManual(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", fetchErr: errors.New("offline")}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.CheckFailures != 1 || s.NextCheck.Sub(clk.t) != 15*time.Minute {
		t.Fatalf("backoff %v", s.NextCheck.Sub(clk.t))
	}
	clk.add(10 * time.Minute)
	p.calls = nil
	if run(t, co); len(p.calls) != 0 {
		t.Fatal("must not refetch before backoff expires")
	}
	p.fetchErr = nil
	p.feed = feedFor("0.1.0-dev.18", "core", 100, "0.1.0-dev.18")
	clk.add(10 * time.Minute)
	if s = run(t, co); s.Target != nil {
		t.Fatal("withdrawn version must not be targeted")
	}
	p.feed = feedFor("0.1.0-dev.16", "core", 100)
	co.Force = true
	if s = run(t, co); s.Target != nil {
		t.Fatal("never downgrade automatically")
	}
	p.feed = feedFor("0.1.0-dev.18", "core", 0)
	if s = run(t, co); s.Target != nil {
		t.Fatal("rollout 0 must exclude every device")
	}
	s.Mode = ModeManual
	Save(co.Base, s)
	co.Force = false
	p.calls = nil
	if run(t, co); len(p.calls) != 0 {
		t.Fatal("manual mode must not check automatically")
	}
}

func TestMeteredNetworkDoesNotBlockDownloadButLowBatteryStillDoes(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100), cond: Conditions{Metered: true, BatteryKnown: true, Battery: 20}}
	co, _ := newCoordinator(t, p)
	if s := run(t, co); s.Blocked == nil || s.Blocked.Reason != "low_battery" || s.Stage != StageFound || strings.Contains(strings.Join(p.calls, " "), "download") {
		t.Fatalf("low battery should retain the target without downloading: %+v %v", s, p.calls)
	}
	p.cond.Charging = true
	if s := run(t, co); s.Stage != StageActive || s.Blocked != nil || !strings.Contains(strings.Join(p.calls, " "), "download") {
		t.Fatalf("metered network should download while charging: %+v %v", s, p.calls)
	}
}

func TestComposingDuringDrainLeavesPendingWithoutLosingInput(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}, drainErr: errors.New("still composing")}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StagePending || s.Blocked.Reason != "engine_busy" {
		t.Fatalf("%s %+v", s.Stage, s.Blocked)
	}
	p.running = "0.1.0-dev.18" // 引擎在下一次自然重啟時使用新版
	clk.add(time.Hour)
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("%s", s.Stage)
	}
}

func TestConcurrentRunsAreSerialized(t *testing.T) {
	co, _ := newCoordinator(t, &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.17", "core", 100)})
	unlock, err := lock(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := co.Run(context.Background()); err == nil {
		t.Fatal("a second coordinator must not run concurrently")
	}
}

type oldShell struct{ *fakePlatform }

func (oldShell) Prepare(context.Context, string, Target) (string, error) { return "", ErrNeedsShell }

func TestCoreUpdateFallsBackToShellWhenShellTooOld(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100), shellErr: ErrNeedsConsent}
	co, _ := newCoordinator(t, p)
	co.Platform = oldShell{p}
	s := run(t, co)
	if s.Target.Kind != "shell" || s.Blocked.Reason != "needs_consent" {
		t.Fatalf("%+v %+v", s.Target, s.Blocked)
	}
}

func TestAskModeWaitsForConsentThenApplies(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}}
	co, clk := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode = ModeAsk
	Save(co.Base, s)
	s = run(t, co)
	if s.Stage != StagePrepared || s.Blocked.Reason != "needs_consent" || len(p.notices) != 1 {
		t.Fatalf("%s %+v %v", s.Stage, s.Blocked, p.notices)
	}
	for _, c := range p.calls {
		if c == "switch" {
			t.Fatal("ask mode must not switch before consent")
		}
	}
	clk.add(time.Minute)
	p.cond.Idle = 0 // 使用者正坐在電腦前執行 finish
	co.Consent = true
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("consent must apply: %s %+v", s.Stage, s.Blocked)
	}
}

func TestShellUpdateDeferredToLoginNotifiesOnce(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.18", running: "0.1.0-dev.18", feed: feedFor("0.1.0-dev.19", "shell", 100), shellErr: ErrAtLogin}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StagePending || s.Blocked.Reason != "needs_relogin" || len(p.notices) != 1 {
		t.Fatalf("%s %+v %v", s.Stage, s.Blocked, p.notices)
	}
	clk.add(time.Hour)
	p.calls = nil
	run(t, co)
	for _, c := range p.calls {
		if c == "shell" {
			t.Fatal("a staged shell update must not be reinstalled by the timer")
		}
	}
	p.running = "0.1.0-dev.19" // 登入時安裝完成、新外殼啟動新引擎
	clk.add(time.Hour)
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("%s", s.Stage)
	}
}

// Windows E23 發現：交接延到下一次喚起時，引擎沒執行也必須啟動並核對，不能直接視為生效。
func TestPendingCoreIsVerifiedNotAssumed(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}, drainErr: errors.New("EOF")}
	co, clk := newCoordinator(t, p)
	s := run(t, co)
	if s.Stage != StagePending || s.Previous != "" && p.pointer == "" {
		t.Fatalf("%s", s.Stage)
	}
	p.drainErr, p.running = nil, "" // 舊引擎之後自行退出
	p.startVer = "0.1.0-dev.18-fault"
	clk.add(time.Hour)
	s = run(t, co)
	if s.Stage != StageFailed || p.pointer != "" || !s.failed("0.1.0-dev.18") {
		t.Fatalf("stage %s pointer %q calls %v", s.Stage, p.pointer, p.calls)
	}
}

type crashAfterSwitch struct {
	*fakePlatform
	base    string
	crashed bool
	t       *testing.T
}

// Installed 與真實平台相同：指標指向的版本視為已安裝。
func (p *crashAfterSwitch) Installed() (string, error) {
	if strings.HasPrefix(p.pointer, "versions/") {
		return strings.TrimSuffix(strings.TrimPrefix(p.pointer, "versions/"), "-x"), nil
	}
	return p.installed, nil
}

// Drain 時檢查狀態已保存，然後模擬行程被中止（panic 跳出，Run 不會存檔）。
func (p *crashAfterSwitch) Drain(ctx context.Context) error {
	if !p.crashed {
		p.crashed = true
		s, err := Load(p.base)
		if err != nil || !s.Switched || p.prevPointer != "orig" {
			p.t.Fatalf("switch must be saved before the handover: %+v %v", s, err)
		}
		panic("killed")
	}
	return p.fakePlatform.Drain(ctx)
}

// E22：切換後、交接前被中止，重跑時仍以原指標為回復目標。
func TestKilledAfterSwitchKeepsOriginalRollbackTarget(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}, pointer: "orig", startVer: "0.1.0-dev.18-fault"}
	co, clk := newCoordinator(t, p)
	cp := &crashAfterSwitch{fakePlatform: p, base: co.Base, t: t}
	co.Platform = cp
	func() {
		defer func() { recover() }()
		co.Run(context.Background())
	}()
	// 被中止的行程留下的鎖：記錄一個已不存在的 PID，協調器應直接接手。
	os.WriteFile(filepath.Join(co.Base, "update", "coordinator.lock"), []byte("999999999\n"), 0o600)
	clk.add(7 * time.Hour) // 重跑時也到了查詢時間
	s := run(t, co)
	if s.Stage != StageFailed || p.pointer != "orig" {
		t.Fatalf("must revert to the original pointer: stage %s pointer %q", s.Stage, p.pointer)
	}
	for _, e := range s.History {
		if e.Kind == "found" && e.At.After(s.History[0].At) {
			t.Fatal("an interrupted switch must be finished, not dropped and rediscovered")
		}
	}
}

func TestConsentNoticeCarriesInstallAction(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellErr: ErrNeedsConsent}
	co, _ := newCoordinator(t, p)
	run(t, co)
	if len(p.actions) != 1 || p.actions[0] != "finish" {
		t.Fatalf("consent notice must offer install: %v", p.actions)
	}
}

type pauseDuringRun struct {
	*fakePlatform
	base string
}

type scheduleDuringRun struct {
	*fakePlatform
	base    string
	removed bool
}

func (p *scheduleDuringRun) Download(ctx context.Context, target Target, dir string) (string, error) {
	if _, err := EditState(p.base, func(s *State) error {
		s.ScheduleRemoved = p.removed
		s.UpdaterBroken, s.UpdaterRepairs = "", nil
		return nil
	}); err != nil {
		return "", err
	}
	return p.fakePlatform.Download(ctx, target, dir)
}

func TestScheduleChoiceChangedDuringDownloadSurvives(t *testing.T) {
	for _, removed := range []bool{true, false} {
		t.Run(fmt.Sprint(removed), func(t *testing.T) {
			p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellErr: ErrNeedsConsent}
			co, _ := newCoordinator(t, p)
			initial, _ := Load(co.Base)
			initial.ScheduleRemoved = !removed
			initial.UpdaterBroken = "previous repair"
			initial.UpdaterRepairs = []time.Time{time.Now()}
			if err := Save(co.Base, initial); err != nil {
				t.Fatal(err)
			}
			co.Platform = &scheduleDuringRun{p, co.Base, removed}
			s := run(t, co)
			saved, err := Load(co.Base)
			if err != nil {
				t.Fatal(err)
			}
			for _, got := range []*State{s, saved} {
				if got.ScheduleRemoved != removed || got.UpdaterBroken != "" || len(got.UpdaterRepairs) != 0 {
					t.Fatalf("download overwrote schedule choice or repair reset: %+v", got)
				}
			}
		})
	}
}

// 選單在協調器執行中改了暫停：Download 期間模擬另一個行程寫入暫停。
func (p *pauseDuringRun) Download(ctx context.Context, t Target, dir string) (string, error) {
	s, _ := Load(p.base)
	s.PausedUntil = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	s.Mode = ModeManual
	Save(p.base, s)
	return p.fakePlatform.Download(ctx, t, dir)
}

// Windows 選單 E2E 發現：協調器結束時寫回整份狀態，會蓋掉執行期間使用者改的暫停與模式。
func TestUserSettingsChangedDuringRunAreKept(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100), cond: Conditions{Metered: false}}
	co, _ := newCoordinator(t, p)
	initial, _ := Load(co.Base)
	Save(co.Base, initial) // 已安裝的裝置都有狀態檔
	co.Platform = &pauseDuringRun{fakePlatform: p, base: co.Base}
	s := run(t, co)
	if s.PausedUntil.IsZero() || s.Mode != ModeManual {
		t.Fatalf("pause and mode set during the run must survive: %v %s", s.PausedUntil, s.Mode)
	}
	saved, _ := Load(co.Base)
	if saved.PausedUntil.IsZero() || saved.Mode != ModeManual {
		t.Fatal("saved state lost the user's settings")
	}
}

// Linux 外殼可重載時才走這條分支，其他平台維持原有權限／登入規則。
type liveShellFake struct {
	*fakePlatform
	base string
	t    *testing.T
}

func (p *liveShellFake) ReloadsShell() bool { return true }
func (p *liveShellFake) InstallShell(ctx context.Context, archive string) (bool, error) {
	s, err := Load(p.base)
	if err != nil || s.Target == nil {
		p.t.Fatalf("安裝前必須先保存目標：%v", err)
	}
	pending, err := p.fakePlatform.InstallShell(ctx, archive)
	if err == nil && !pending {
		p.installed = s.Target.Version
		p.running = s.Target.Version
	}
	return pending, err
}
func TestLiveShellWaitsForIdleThenVerifiesWithoutLogin(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), cond: Conditions{IdleKnown: true, Idle: time.Second}}
	co, _ := newCoordinator(t, p)
	co.Platform = &liveShellFake{p, co.Base, t}
	s := run(t, co)
	if s.Stage != StagePrepared || s.Blocked.Reason != "waiting_idle" || strings.Contains(strings.Join(p.calls, " "), "shell") {
		t.Fatalf("%+v %v", s, p.calls)
	}
	p.cond.Idle = 6 * time.Minute
	p.calls = nil
	s = run(t, co)
	if s.Stage != StageActive || s.Blocked != nil || strings.Join(p.calls, " ") != "shell" {
		t.Fatalf("%+v %v", s, p.calls)
	}
}
func TestManualFinishCanApplyLiveShellWhileBusyTimeAndManualMode(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), cond: Conditions{IdleKnown: true, Idle: time.Second}}
	co, _ := newCoordinator(t, p)
	co.Platform = &liveShellFake{p, co.Base, t}
	run(t, co)
	s, _ := Load(co.Base)
	s.Mode = ModeManual
	_ = Save(co.Base, s)
	co.Consent = true
	if s = run(t, co); s.Stage != StageActive || s.Mode != ModeManual {
		t.Fatalf("%+v", s)
	}
}
func TestLiveShellKeepsCompositionAndRetries(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), cond: Conditions{IdleKnown: true, Idle: 6 * time.Minute}, shellErr: ErrShellBusy}
	co, _ := newCoordinator(t, p)
	co.Platform = &liveShellFake{p, co.Base, t}
	s := run(t, co)
	if s.Stage != StagePrepared || s.Blocked.Reason != "engine_busy" || p.running != "0.1.0-dev.40" {
		t.Fatalf("%+v", s)
	}
	p.shellErr = nil
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("%+v", s)
	}
}
func TestLiveShellTakesOverOldPendingAndDoesNotTrustEngineAlone(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.41", feed: feedFor("0.1.0-dev.41", "shell", 100), cond: Conditions{IdleKnown: true, Idle: 6 * time.Minute}, shellErr: ErrShellBusy}
	co, _ := newCoordinator(t, p)
	co.Platform = &liveShellFake{p, co.Base, t}
	s := run(t, co)
	s.Stage = StagePending
	s.block(co.Now(), "needs_relogin", "登出再登入時自動完成安裝")
	_ = Save(co.Base, s)
	p.calls = nil
	s = run(t, co)
	if s.Stage != StagePending || s.Blocked.Reason != "engine_busy" || strings.Join(p.calls, " ") != "shell" {
		t.Fatalf("%+v %v", s, p.calls)
	}
	p.shellErr = nil
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("%+v", s)
	}
}

func TestExplicitCheckInManualModePreparesButWaitsForFinish(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40", feed: feedFor("0.1.0-dev.41", "shell", 100), cond: Conditions{IdleKnown: true, Idle: time.Hour}}
	co, _ := newCoordinator(t, p)
	co.Platform = &liveShellFake{p, co.Base, t}
	s, _ := Load(co.Base)
	s.Mode = ModeManual
	_ = Save(co.Base, s)
	co.Force = true
	s = run(t, co)
	if s.Mode != ModeManual || s.Stage != StagePrepared || s.Blocked.Reason != "needs_consent" || strings.Join(p.calls, " ") != "fetch download" {
		t.Fatalf("%+v %v", s, p.calls)
	}
	co.Force = false
	co.Consent = true
	if s = run(t, co); s.Stage != StageActive || s.Mode != ModeManual {
		t.Fatalf("%+v", s)
	}
}

type recoveredShellFake struct{ *liveShellFake }

func (p *recoveredShellFake) RecoverShell(context.Context) (bool, error) {
	p.calls = append(p.calls, "recover")
	s, err := Load(p.base)
	if err != nil {
		return false, err
	}
	p.running = s.Target.Version
	p.installed = s.Target.Version
	return true, nil
}
func TestRecoveredShellBecomesActiveEvenInManualMode(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.40", running: "0.1.0-dev.40"}
	co, _ := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode = ModeManual
	s.Stage = StagePrepared
	s.Target = &Target{Version: "0.1.0-dev.41", Kind: "shell"}
	_ = Save(co.Base, s)
	co.Platform = &recoveredShellFake{&liveShellFake{p, co.Base, t}}
	if s = run(t, co); s.Stage != StageActive || s.Target != nil || s.Mode != ModeManual || strings.Join(p.calls, " ") != "recover" {
		t.Fatalf("%+v %v", s, p.calls)
	}
}

type loginShellPlatform struct {
	*fakePlatform
	ready    bool
	readyErr error
}

func (p *loginShellPlatform) ShellReady(context.Context, string) (bool, error) {
	return p.ready, p.readyErr
}

func TestShellRequiresNewLoginEvenWhenNewEngineIsRunning(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.18", feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true}
	co, clk := newCoordinator(t, p)
	lp := &loginShellPlatform{fakePlatform: p}
	co.Platform = lp
	s := run(t, co)
	p.installed = p.feed.Version
	since := s.Blocked.Since
	for _, queryErr := range []error{nil, errors.New("session unavailable")} {
		lp.readyErr = queryErr
		clk.add(7 * time.Hour)
		s = run(t, co)
		if s.Stage != StagePending || s.Target == nil || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" || !s.Blocked.Since.Equal(since) {
			t.Fatalf("same login falsely activated shell: %+v", s)
		}
	}
	lp.ready, lp.readyErr = true, nil
	clk.add(time.Minute)
	s = run(t, co)
	if s.Stage != StageActive || s.Target != nil || s.Blocked != nil {
		t.Fatalf("new login should activate: %+v", s)
	}
}

type provingShellPlatform struct{ *loginShellPlatform }

func (provingShellPlatform) ShellLoadProven() bool { return true }

// #77：新外殼已確認載入時，引擎剛好閒置結束不應讓「重新登入後生效」一直留著；舊引擎還在跑時仍要等。
func TestConfirmedShellActivatesWhileEngineIsIdle(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true}
	co, clk := newCoordinator(t, p)
	lp := &loginShellPlatform{fakePlatform: p}
	co.Platform = provingShellPlatform{lp}
	run(t, co)
	p.installed = p.feed.Version
	lp.ready = true
	clk.add(time.Minute)
	if s := run(t, co); s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" {
		t.Fatalf("old engine still running must wait: %+v", s)
	}
	p.running = ""
	clk.add(time.Minute)
	if s := run(t, co); s.Stage != StageActive || s.Target != nil || s.Blocked != nil {
		t.Fatalf("idle engine after confirmed shell should activate: %+v", s)
	}
}

// #98：重新登入後還沒打字（外殼與引擎都沒在跑）時，平台確認沒有舊外殼就生效；手動模式、暫停中，
// 或只按「檢查更新」也要重新核對，不能一直顯示「等待重新登入後生效」。
func TestPendingShellConfirmedAfterReloginWithoutTyping(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Coordinator, *State)
		check bool
	}{
		{"auto", func(*Coordinator, *State) {}, false},
		{"manual", func(_ *Coordinator, s *State) { s.Mode = ModeManual }, false},
		{"paused", func(co *Coordinator, s *State) { s.PausedUntil = co.Now().Add(24 * time.Hour) }, false},
		{"check-button", func(_ *Coordinator, s *State) { s.Mode = ModeManual }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePlatform{installed: "0.1.0-dev.54", running: "0.1.0-dev.54", feed: feedFor("0.1.0-dev.59", "shell", 100), shellPend: true}
			co, clk := newCoordinator(t, p)
			lp := &loginShellPlatform{fakePlatform: p}
			co.Platform = provingShellPlatform{lp}
			if s := run(t, co); s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" {
				t.Fatalf("install should wait for a new login: %+v", s)
			}
			p.installed = p.feed.Version
			s, _ := Load(co.Base)
			tc.setup(co, s)
			_ = Save(co.Base, s)
			// 重新開機、登入，還沒用靈犀打字：引擎沒有執行，平台也沒看到舊外殼。
			lp.ready, p.running = true, ""
			clk.add(time.Minute)
			p.calls = nil
			if tc.check {
				var err error
				if s, err = co.Check(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				s = run(t, co)
			}
			if s.Stage != StageActive || s.Target != nil || s.Blocked != nil {
				t.Fatalf("pending shell not confirmed after relogin: %+v", s)
			}
			if slices.Contains(p.calls, "install") || slices.Contains(p.calls, "download") {
				t.Fatalf("confirmation must stay local: %v", p.calls)
			}
			if v := ViewOf(s, p.installed); v.Target != nil || v.Stage != StageActive {
				t.Fatalf("view still shows the pending shell: %+v", v)
			}
		})
	}
}

// #98：重新登入後仍有舊版外殼（例如系統開到 backups 裡的舊 App）時，繼續等待並說明原因；
// 舊外殼結束、下一次登入後生效。
func TestPendingShellExplainsOldShellStillLoaded(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.54", running: "", feed: feedFor("0.1.0-dev.59", "shell", 100), shellPend: true}
	co, clk := newCoordinator(t, p)
	lp := &loginShellPlatform{fakePlatform: p}
	co.Platform = provingShellPlatform{lp}
	run(t, co)
	p.installed = p.feed.Version
	since := mustLoad(t, co).Blocked.Since
	lp.readyErr = fmt.Errorf("%w：/Users/test-user/Library/Application Support/0c/backups/macos-1/0c.app", ErrOldShellLoaded)
	clk.add(time.Hour)
	s, err := co.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" || s.Blocked.Detail != OldShellDetail || !s.Blocked.Since.Equal(since) {
		t.Fatalf("old shell should keep the pending shell with an explanation: %+v", s.Blocked)
	}
	lp.ready, lp.readyErr = true, nil
	clk.add(time.Minute)
	if s = run(t, co); s.Stage != StageActive || s.Blocked != nil {
		t.Fatalf("shell should activate once the old copy is gone: %+v", s)
	}
}

func mustLoad(t *testing.T, co *Coordinator) *State {
	t.Helper()
	s, err := Load(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 只核對登入邊界的平台（Windows）仍要等新版引擎實際回報版本，引擎閒置不算生效。
func TestLoginOnlyShellStillWaitsForRunningEngine(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true}
	co, clk := newCoordinator(t, p)
	lp := &loginShellPlatform{fakePlatform: p}
	co.Platform = lp
	run(t, co)
	p.installed = p.feed.Version
	lp.ready = true
	p.running = ""
	clk.add(time.Minute)
	if s := run(t, co); s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "needs_relogin" {
		t.Fatalf("login-only platform must not activate on an idle engine: %+v", s)
	}
	p.running = "0.1.0-dev.18"
	clk.add(time.Minute)
	if s := run(t, co); s.Stage != StageActive {
		t.Fatalf("running new engine should activate: %+v", s)
	}
}

func TestPendingShellDefersNewCoreUntilBothLoginAndRuntimeMatch(t *testing.T) {
	for _, logonReady := range []bool{false, true} {
		t.Run(fmt.Sprint(logonReady), func(t *testing.T) {
			p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true, cond: Conditions{IdleKnown: true, Idle: 6 * time.Minute}}
			co, clk := newCoordinator(t, p)
			lp := &loginShellPlatform{fakePlatform: p, ready: logonReady}
			co.Platform = lp
			run(t, co)
			p.installed = "0.1.0-dev.18"
			p.feed = feedFor("0.1.0-dev.19", "core", 100)
			clk.add(7 * time.Hour)
			p.calls = nil
			s := run(t, co)
			if s.Target == nil || s.Target.Version != "0.1.0-dev.18" || s.Stage != StagePending || strings.Join(p.calls, " ") != "fetch" {
				t.Fatalf("core erased pending shell: %+v calls=%v", s, p.calls)
			}
			lp.ready = true
			p.running = "0.1.0-dev.18"
			clk.add(7 * time.Hour)
			s = run(t, co)
			if s.Stage != StageActive || p.pointer != "versions/0.1.0-dev.19-x" {
				t.Fatalf("core did not resume after activation: %+v", s)
			}
			var activated []string
			for _, e := range s.History {
				if e.Kind == "active" {
					activated = append(activated, e.Version)
				}
			}
			if strings.Join(activated, " ") != "0.1.0-dev.18 0.1.0-dev.19" {
				t.Fatalf("activation history=%v", activated)
			}
		})
	}
}

func TestPendingShellAllowsNewerFullShell(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "shell", 100), shellPend: true}
	co, clk := newCoordinator(t, p)
	co.Platform = &loginShellPlatform{fakePlatform: p}
	run(t, co)
	p.installed = "0.1.0-dev.18"
	p.feed = feedFor("0.1.0-dev.19", "shell", 100)
	clk.add(7 * time.Hour)
	s := run(t, co)
	if s.Stage != StagePending || s.Target == nil || s.Target.Version != "0.1.0-dev.19" {
		t.Fatalf("new shell cannot supersede pending shell: %+v", s)
	}
}
