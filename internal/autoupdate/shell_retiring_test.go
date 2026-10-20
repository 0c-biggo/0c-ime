package autoupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #170（macOS）：安裝時已請舊的輸入法程式結束，不必重新登入。舊程式還在（使用者正在打字）時說明停手後自動換新，
// 不要求登出、不發通知；舊程式結束後同一個登入工作階段裡就生效。
func TestShellRetiringActivatesWithoutRelogin(t *testing.T) {
	p := &fakePlatform{installed: "0.2.0", running: "0.2.0", feed: feedFor("0.2.1", "shell", 100), shellPend: false}
	co, clk := newCoordinator(t, p)
	lp := &loginShellPlatform{fakePlatform: p}
	co.Platform = provingShellPlatform{lp}
	lp.readyErr = fmt.Errorf("%w：/Users/lily/Library/Input Methods/0c.app", ErrShellRetiring)
	// 安裝器交接過引擎；新版引擎已在跑。
	p.installHook = func() { p.installed, p.running = p.feed.Version, p.feed.Version }
	s := run(t, co)
	if s.Stage != StagePending || s.Blocked == nil || s.Blocked.Reason != "waiting_idle" || s.Blocked.Detail != ShellRetiringDetail {
		t.Fatalf("old shell still running should wait without relogin: %+v %+v", s, s.Blocked)
	}
	if len(p.notices) != 0 {
		t.Fatalf("waiting for the old shell to retire is not a notice: %v", p.notices)
	}
	clk.add(25 * time.Hour)
	if s = run(t, co); len(p.notices) != 0 || s.Blocked == nil || s.Blocked.Detail != ShellRetiringDetail {
		t.Fatalf("no reminder while retiring: %v %+v", p.notices, s.Blocked)
	}
	lp.readyErr = nil
	lp.ready = true
	clk.add(15 * time.Minute)
	if s = run(t, co); s.Stage != StageActive || s.Target != nil || s.Blocked != nil {
		t.Fatalf("shell should activate in the same session once the old shell is gone: %+v", s)
	}
}

// 安裝時沒有舊程式要換（或已結束）：裝完當下就生效。
func TestShellWithoutOldProcessActivatesRightAway(t *testing.T) {
	p := &fakePlatform{installed: "0.2.0", running: "0.2.0", feed: feedFor("0.2.1", "shell", 100), shellPend: false}
	co, _ := newCoordinator(t, p)
	lp := &loginShellPlatform{fakePlatform: p, ready: true}
	co.Platform = provingShellPlatform{lp}
	p.installHook = func() { p.installed, p.running = p.feed.Version, "" }
	if s := run(t, co); s.Stage != StageActive || s.Blocked != nil || len(p.notices) != 0 {
		t.Fatalf("%+v %v", s, p.notices)
	}
}

// 設定程式以完整文字辨識「停手後自動換新」，兩邊要一起改。
func TestSettingsUIRecognizesShellRetiringDetail(t *testing.T) {
	for _, name := range []string{"app.js", "engine-state.js"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "platforms", "settings", "ui", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "'"+ShellRetiringDetail+"'") {
			t.Fatalf("%s does not match ShellRetiringDetail %q", name, ShellRetiringDetail)
		}
	}
}
