package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// fakeStartMenu 模擬 Windows 的開始功能表維護：真的寫檔，只把 .lnk 換成一般檔案、收據換成 hook。
type fakeStartMenu struct {
	t         *testing.T
	base      string
	startMenu string
	source    string // 目前版本的設定程式
	version   string // 收據版本；空字串＝收據已停用（解除安裝）
	missing   bool   // 目前版本沒有附設定程式
	creates   int
}

func newFakeStartMenu(t *testing.T) *fakeStartMenu {
	t.Helper()
	root := t.TempDir()
	f := &fakeStartMenu{t: t, base: filepath.Join(root, "Zuin"), startMenu: filepath.Join(root, "StartMenu"), version: "0.2.12"}
	f.source = filepath.Join(f.base, "versions", "0.2.12-x", "0c-settings.exe")
	writeFile(t, f.source, "settings 0.2.12")
	writeFile(t, stableUpdater(f.base), "updater")
	return f
}

func (f *fakeStartMenu) entry(restore bool) startMenuEntry {
	return startMenuEntry{
		base:      f.base,
		restore:   restore,
		installed: func() (string, error) { return f.version, nil },
		locate: func() (string, string, error) {
			if f.missing {
				return "", "", settingsapp.ErrNotFound
			}
			h, err := update.Hash(f.source)
			return f.source, h, err
		},
		spec: func(stable string) (settingsapp.ShortcutSpec, error) {
			return settingsapp.NewShortcutSpec(f.startMenu, f.base, stable)
		},
		ensure: func(spec settingsapp.ShortcutSpec, restore bool) (bool, error) {
			return settingsapp.EnsureShortcut(f.base, spec, restore, func(spec settingsapp.ShortcutSpec) error {
				f.creates++
				return os.WriteFile(spec.Path, []byte("lnk → "+spec.Target+" "+spec.Args), 0o644)
			})
		},
	}
}

func (f *fakeStartMenu) lnk() string {
	return filepath.Join(f.startMenu, settingsapp.ShortcutFolder, settingsapp.ShortcutName)
}

func (f *fakeStartMenu) stableContent() string {
	b, err := os.ReadFile(stableSettings(f.base))
	if err != nil {
		return ""
	}
	return string(b)
}

// #284 主流程：建立 → 指向穩定副本 → 版本更新跟著換 → 被防毒軟體隔離後，每 15 分鐘的排程不再重建 → 重新安裝補回。
func TestStartMenuEntryIsNotRebuiltAfterItWasRemoved(t *testing.T) {
	f := newFakeStartMenu(t)
	f.entry(true).sync() // 完整安裝
	if f.creates != 1 || !regularFile(f.lnk()) {
		t.Fatalf("安裝要建立捷徑：creates=%d", f.creates)
	}
	if got := f.stableContent(); got != "settings 0.2.12" {
		t.Fatalf("穩定副本 = %q", got)
	}
	b, _ := os.ReadFile(f.lnk())
	if want := "lnk → " + stableSettings(f.base) + " "; string(b) != want {
		t.Fatalf("捷徑要直接指向穩定副本：%q", b)
	}

	// 核心更新帶來新版設定程式：穩定副本換新，捷徑內容不變所以不重寫。
	writeFile(t, f.source, "settings 0.2.13")
	f.entry(false).sync()
	if f.stableContent() != "settings 0.2.13" || f.creates != 1 {
		t.Fatalf("版本更新：穩定副本 %q、creates=%d", f.stableContent(), f.creates)
	}

	// SEP 隔離了捷徑；之後一天的自動更新排程都不可重建。
	if err := os.Remove(f.lnk()); err != nil {
		t.Fatal(err)
	}
	for range 96 {
		f.entry(false).sync()
	}
	if f.creates != 1 || regularFile(f.lnk()) {
		t.Fatalf("被移除的捷徑由排程重建了：creates=%d", f.creates)
	}

	// 使用者重新安裝：補回。
	f.entry(true).sync()
	if f.creates != 2 || !regularFile(f.lnk()) {
		t.Fatalf("重新安裝要補回：creates=%d", f.creates)
	}
}

// 捷徑指向的穩定副本被隔離：移除捷徑（不留下點了會出錯的項目），也不一直把副本複製回來。
func TestStartMenuEntryWithdrawsWhenTheStableCopyIsQuarantined(t *testing.T) {
	f := newFakeStartMenu(t)
	f.entry(true).sync()
	if err := os.Remove(stableSettings(f.base)); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		f.entry(false).sync()
	}
	if regularFile(stableSettings(f.base)) || regularFile(f.lnk()) || f.creates != 1 {
		t.Fatalf("副本被隔離後：副本 %v、捷徑 %v、creates=%d", regularFile(stableSettings(f.base)), regularFile(f.lnk()), f.creates)
	}
	f.entry(true).sync()
	if !regularFile(stableSettings(f.base)) || !regularFile(f.lnk()) {
		t.Fatal("重新安裝要補回副本與捷徑")
	}
}

// 舊版（0.2.11 以前）建立的 conhost --headless 捷徑：更新後由排程換成新樣式一次，即使舊捷徑已被隔離。
func TestStartMenuEntryReplacesTheLegacyConhostShortcut(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		f := newFakeStartMenu(t)
		legacy, err := settingsapp.NewShortcutSpec(f.startMenu, f.base, stableUpdater(f.base))
		if err != nil {
			t.Fatal(err)
		}
		legacy.Target = `C:\Windows\System32\conhost.exe`
		legacy.Args = `--headless "` + stableUpdater(f.base) + `" open-settings`
		legacy.Dir = f.base
		if _, err = settingsapp.EnsureShortcut(f.base, legacy, false, func(s settingsapp.ShortcutSpec) error {
			return os.WriteFile(s.Path, []byte("legacy"), 0o644)
		}); err != nil {
			t.Fatal(err)
		}
		if quarantined {
			os.Remove(f.lnk())
		}
		f.entry(false).sync() // 新版的自動更新排程
		b, _ := os.ReadFile(f.lnk())
		if want := "lnk → " + stableSettings(f.base) + " "; string(b) != want || f.creates != 1 {
			t.Fatalf("quarantined=%v：舊樣式要換成新樣式：%q creates=%d", quarantined, b, f.creates)
		}
		f.entry(false).sync()
		if f.creates != 1 {
			t.Fatal("換過一次就不再重寫")
		}
	}
}

// 解除安裝後（收據停用）：即使 engine.json 還指著核心目錄的設定程式，也只移除、不重建。
func TestStartMenuEntryIsRemovedAndStaysRemovedAfterUninstall(t *testing.T) {
	f := newFakeStartMenu(t)
	f.entry(true).sync()
	f.version = ""
	f.entry(false).sync()
	if regularFile(f.lnk()) || regularFile(stableSettings(f.base)) {
		t.Fatal("收據停用後要移除捷徑與穩定副本")
	}
	if _, err := os.Stat(filepath.Join(f.base, settingsapp.ShortcutMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("標記檔也要移除")
	}
	for range 4 {
		f.entry(false).sync()
		f.entry(true).sync() // 就算有人以 restore 呼叫也一樣
	}
	if f.creates != 1 || regularFile(f.lnk()) {
		t.Fatalf("解除安裝後重建了捷徑：creates=%d", f.creates)
	}
}

// 回到沒有附設定程式的版本：移除捷徑與穩定副本。
func TestStartMenuEntryFollowsAVersionWithoutSettings(t *testing.T) {
	f := newFakeStartMenu(t)
	f.entry(true).sync()
	f.missing = true
	f.entry(false).sync()
	if regularFile(f.lnk()) || regularFile(stableSettings(f.base)) {
		t.Fatal("沒有設定程式的版本不留入口")
	}
}

// #284：解除安裝之後（收據停用、bin 因執行中的穩定更新器刪不掉而留著），schedule repair 不可把排程建回來。
func TestRepairDoesNotRecreateTheScheduleAfterUninstall(t *testing.T) {
	f := newFakeRepair(t)
	f.schedule = false // 解除安裝刪了排程
	h := f.hooks()
	h.uninstalled = func() bool { return true }
	if err := repairUpdater(f.base, true, h); err != nil || f.installs != 0 || len(f.notices) != 0 {
		t.Fatalf("解除安裝後 repair 不可重建或通知：err=%v installs=%d notices=%d", err, f.installs, len(f.notices))
	}
	h.uninstalled = func() bool { return false }
	f.now = f.now.Add(time.Minute)
	if err := repairUpdater(f.base, true, h); err != nil || f.installs != 1 {
		t.Fatalf("仍安裝時照舊重建：err=%v installs=%d", err, f.installs)
	}
}
