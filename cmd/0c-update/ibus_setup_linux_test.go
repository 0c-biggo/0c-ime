//go:build !windows && !darwin

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeTools 讓 exec.LookPath 只看得到 names。
func fakeTools(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755)
	}
	t.Setenv("PATH", dir)
}

func TestInputFrameworkFollowsTheDesktopInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GTK_IM_MODULE", "")
	shell := filepath.Join(home, ibusShellPath)
	os.MkdirAll(filepath.Dir(shell), 0o755)
	os.WriteFile(shell, []byte("shell"), 0o755)

	fakeTools(t, "fcitx5", "ibus-daemon")
	fakeProc(t, map[int][3]any{40: {"fcitx5", "S", 1}, 41: {"ibus-daemon", "S", 1}})
	if got := inputFramework(); got != "fcitx" {
		t.Fatalf("Fcitx5 執行中 = %q", got)
	}
	// Ubuntu／GNOME：IBus 在跑、Fcitx5 只是裝著。
	fakeProc(t, map[int][3]any{41: {"ibus-daemon", "S", 1}})
	if got := inputFramework(); got != frontendIBus {
		t.Fatalf("IBus 執行中 = %q", got)
	}
	// 之前就以 Fcitx5 使用靈犀注音的人，Fcitx5 暫時沒在跑也不改成 IBus。
	profile := filepath.Join(home, ".config/fcitx5/profile")
	os.MkdirAll(filepath.Dir(profile), 0o755)
	os.WriteFile(profile, []byte("[Groups/0/Items/1]\n# Name\nName=zuin\n"), 0o644)
	if got := inputFramework(); got != "fcitx" {
		t.Fatalf("已在 Fcitx5 清單 = %q", got)
	}
	// Fcitx5 已經移除時，舊的 profile 不算數。
	fakeTools(t, "ibus-daemon")
	if got := inputFramework(); got != frontendIBus {
		t.Fatalf("Fcitx5 已移除、只剩舊 profile = %q", got)
	}
	fakeTools(t, "fcitx5", "ibus-daemon")
	os.Remove(profile)
	// 都沒在跑：看裝了哪一個；套件沒有 IBus 外殼時不選 IBus。
	fakeProc(t, map[int][3]any{})
	fakeTools(t, "ibus-daemon")
	if got := inputFramework(); got != frontendIBus {
		t.Fatalf("只裝 IBus = %q", got)
	}
	os.Remove(shell)
	if got := inputFramework(); got != "" {
		t.Fatalf("沒有 IBus 外殼 = %q", got)
	}
	fakeTools(t, "fcitx5")
	if got := inputFramework(); got != "fcitx" {
		t.Fatalf("只裝 Fcitx5 = %q", got)
	}
}

func TestIBusAddressesPreferTheShellThenNewestFile(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dir := filepath.Join(config, "ibus/bus")
	os.MkdirAll(dir, 0o755)
	old, now := filepath.Join(dir, "id-unix-0"), filepath.Join(dir, "id-unix-wayland-0")
	os.WriteFile(old, []byte("# comment\nIBUS_ADDRESS=unix:path=/old\nIBUS_DAEMON_PID=1\n"), 0o644)
	os.WriteFile(now, []byte("IBUS_ADDRESS=unix:path=/now\n"), 0o644)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)
	got := ibusAddresses("unix:path=/saved")
	if !slices.Equal(got, []string{"unix:path=/saved", "unix:path=/now", "unix:path=/old"}) {
		t.Fatalf("位址 = %v", got)
	}
	if got := ibusAddresses("unix:path=/now"); !slices.Equal(got, []string{"unix:path=/now", "unix:path=/old"}) {
		t.Fatalf("不重複 = %v", got)
	}
}

func TestEngineNameReadsTheSerializedDescription(t *testing.T) {
	desc := dbus.MakeVariant([]interface{}{"IBusEngineDesc", map[string]dbus.Variant{}, "xkb:us::eng", "English"})
	if got := engineName(desc); got != "xkb:us::eng" {
		t.Fatalf("name = %q", got)
	}
	if got := engineName(dbus.MakeVariant("broken")); got != "" {
		t.Fatalf("壞資料 = %q", got)
	}
}

// 外殼退出前不是選用中的輸入法、或登錄已撤銷（回復到沒有 IBus 外殼的版本）：不叫起外殼，之後只核對檔案。
func TestRestoreIBusOnlyWhenTheShellWasActiveAndRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := restoreIBus(t.Context(), &shellRestart{Frontend: frontendIBus}); err != nil {
		t.Fatal(err)
	}
	s := &shellRestart{Frontend: frontendIBus, IM: ibusEngine}
	if err := restoreIBus(t.Context(), s); err != nil || s.IM != "" {
		t.Fatalf("登錄已撤銷：err=%v IM=%q", err, s.IM)
	}
	component := filepath.Join(home, ibusComponent)
	os.MkdirAll(filepath.Dir(component), 0o755)
	os.WriteFile(component, []byte("<component/>"), 0o644)
	if err := restoreIBus(t.Context(), &shellRestart{Frontend: frontendIBus, IM: ibusEngine}); err == nil {
		t.Fatal("找不到 ibus-daemon 時要回報，不能假裝已恢復")
	}
}

func TestIBusToolkitsSyncOnlyWhenRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	log := filepath.Join(t.TempDir(), "calls")
	shell := filepath.Join(home, ibusShellPath)
	os.MkdirAll(filepath.Dir(shell), 0o755)
	os.WriteFile(shell, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n"), 0o755)
	syncIBusToolkits()
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("沒登錄 IBus 時不該呼叫外殼")
	}
	component := filepath.Join(home, ibusComponent)
	os.MkdirAll(filepath.Dir(component), 0o755)
	os.WriteFile(component, []byte("<component/>"), 0o644)
	syncIBusToolkits()
	if b, _ := os.ReadFile(log); string(b) != "toolkits\n" {
		t.Fatalf("外殼收到 %q，要 toolkits", b)
	}
}
