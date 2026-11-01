package main

import (
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// useWindowsRules：這幾段判斷是純邏輯，Linux、macOS 上也跑得到（#253）。
func useWindowsRules(t *testing.T, on bool) {
	t.Helper()
	saved := shellWithoutRelogin
	shellWithoutRelogin = on
	t.Cleanup(func() { shellWithoutRelogin = saved })
}

func pendingShellState(reason, detail string) *autoupdate.State {
	return &autoupdate.State{Schema: 1, InstallID: "test", Mode: autoupdate.ModeAuto, Stage: autoupdate.StagePending,
		Target:  &autoupdate.Target{Version: "0.2.8", Kind: "shell"},
		Blocked: &autoupdate.Blocked{Reason: reason, Detail: detail}}
}

func TestOutcomeBodyAfterInstallingWithoutRelogin(t *testing.T) {
	useWindowsRules(t, true)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	done := &autoupdate.State{Stage: autoupdate.StageActive, History: []autoupdate.Event{
		{At: now.Add(-time.Minute), Kind: "installed", Version: "0.2.8"},
		{At: now.Add(-time.Minute), Kind: "active", Version: "0.2.8"},
	}}
	got := outcomeBody(done, nil, "0.2.8", now)
	for _, want := range []string{"已更新至 0.2.8", "不必登出", "重新開啟"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 缺 %q", got, want)
		}
	}
	// 之後才按「檢查更新」：照常說已是最新，不再提重開。
	if got = outcomeBody(done, nil, "0.2.8", now.Add(time.Hour)); got != "已是最新版本 0.2.8。" {
		t.Fatal(got)
	}
	// 核心更新沒有換 DLL，不用重開程式。
	core := &autoupdate.State{Stage: autoupdate.StageActive, History: []autoupdate.Event{{At: now, Kind: "switched", Version: "0.2.8"}, {At: now, Kind: "active", Version: "0.2.8"}}}
	if got = outcomeBody(core, nil, "0.2.8", now); got != "已是最新版本 0.2.8。" {
		t.Fatal(got)
	}
	// 新引擎還沒接手：照實說，不說完成。
	busy := pendingShellState("engine_busy", autoupdate.ShellEngineBusyDetail)
	if got = outcomeBody(busy, nil, "0.2.8", now); got != "0.2.8 "+autoupdate.ShellEngineBusyDetail+"。" {
		t.Fatal(got)
	}
}

// 選單「已安裝，閒置時換上這一版的引擎」按下去沒換成：沒有待辦（Target 是 nil、Stage 是 idle），不能說已是最新。
func TestOutcomeBodyWhenStaleEngineWasNotReplaced(t *testing.T) {
	useWindowsRules(t, true)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, detail := range []string{autoupdate.ShellEngineBusyDetail, autoupdate.ShellEngineIdleDetail, autoupdate.ShellEngineFailedDetail} {
		for _, stage := range []string{autoupdate.StageIdle, autoupdate.StageActive} {
			s := &autoupdate.State{Stage: stage, StaleEngine: detail}
			if got := outcomeBody(s, nil, "0.2.8", now); got != "0.2.8 "+detail+"。" {
				t.Fatal(got)
			}
		}
	}
	// 剛裝好但舊引擎還在：也不說「已更新至」。
	s := &autoupdate.State{Stage: autoupdate.StageActive, StaleEngine: autoupdate.ShellEngineBusyDetail,
		History: []autoupdate.Event{{At: now.Add(-time.Minute), Kind: "installed", Version: "0.2.8"}}}
	if got := outcomeBody(s, nil, "0.2.8", now); got != "0.2.8 "+autoupdate.ShellEngineBusyDetail+"。" {
		t.Fatal(got)
	}
	// 換好了照舊。
	if got := outcomeBody(&autoupdate.State{Stage: autoupdate.StageIdle}, nil, "0.2.8", now); got != "已是最新版本 0.2.8。" {
		t.Fatal(got)
	}
}

// macOS／Linux：原本的文字不變。
func TestOutcomeBodyKeepsOtherPlatforms(t *testing.T) {
	useWindowsRules(t, false)
	now := time.Now()
	done := &autoupdate.State{Stage: autoupdate.StageActive, History: []autoupdate.Event{{At: now, Kind: "installed", Version: "0.2.8"}}}
	if got := outcomeBody(done, nil, "0.2.8", now); got != "已是最新版本 0.2.8。" {
		t.Fatal(got)
	}
	if got := outcomeBody(pendingShellState("needs_relogin", "重新登入後生效"), nil, "0.2.8", now); got != "0.2.8 重新登入後生效。" {
		t.Fatal(got)
	}
	busy := pendingShellState("engine_busy", "x")
	if got := outcomeBody(busy, nil, "0.2.8", now); !strings.Contains(got, "請先完成組字") {
		t.Fatal(got)
	}
}

// 已裝好的外殼只差新引擎接手：選單可以按「現在完成」，而且不用再跳一次 UAC。
func TestPendingShellNeedsNoElevationOnceInstalled(t *testing.T) {
	useWindowsRules(t, true)
	base := t.TempDir()
	save := func(s *autoupdate.State) {
		if err := autoupdate.Save(base, s); err != nil {
			t.Fatal(err)
		}
	}
	prepared := pendingShellState("needs_consent", "")
	prepared.Stage = autoupdate.StagePrepared
	save(prepared)
	if !pendingShell(base) {
		t.Fatal("還沒裝好的外殼要系統管理員")
	}
	busy := pendingShellState("waiting_idle", autoupdate.ShellEngineIdleDetail)
	save(busy)
	if pendingShell(base) {
		t.Fatal("已裝好的外殼不用再要系統管理員")
	}
	if actionFor(busy) != "finish" {
		t.Fatal("選單要能立刻完成")
	}
}

// 舊版更新器留下的狀態：讀出來就不再叫人登出（舊版 DLL 會直接顯示這段說明）；狀態檔由協調器收尾。macOS 照舊。
func TestHideLegacyRelogin(t *testing.T) {
	useWindowsRules(t, true)
	s := pendingShellState("needs_relogin", "已安裝，重新登入後生效")
	stored := s.Blocked
	hideLegacyRelogin(s)
	if s.Blocked.Reason != "waiting_idle" || s.Blocked.Detail != autoupdate.ShellEngineIdleDetail {
		t.Fatalf("%+v", s.Blocked)
	}
	if stored.Reason != "needs_relogin" {
		t.Fatal("只改讀出來的這一份")
	}
	useWindowsRules(t, false)
	s = pendingShellState("needs_relogin", "已安裝，重新登入後生效")
	hideLegacyRelogin(s)
	if s.Blocked.Reason != "needs_relogin" {
		t.Fatalf("%+v", s.Blocked)
	}
}
