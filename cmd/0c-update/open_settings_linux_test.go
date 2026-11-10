//go:build !windows && !darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func TestInstallHintFollowsTheDistribution(t *testing.T) {
	for _, c := range []struct{ release, want string }{
		{"ID=ubuntu\nID_LIKE=debian\n", "sudo apt install libwebkit2gtk-4.1-0"},
		{"ID=debian\n", "sudo apt install libwebkit2gtk-4.1-0"},
		{"ID=\"linuxmint\"\nID_LIKE=\"ubuntu debian\"\n", "sudo apt install libwebkit2gtk-4.1-0"},
		{"ID=fedora\n", "sudo dnf install webkit2gtk4.1"},
		{"ID=rocky\nID_LIKE=\"rhel centos fedora\"\n", "sudo dnf install webkit2gtk4.1"},
		{"ID=arch\n", "sudo pacman -S webkit2gtk-4.1"},
		{"ID=opensuse-tumbleweed\nID_LIKE=\"opensuse suse\"\n", "sudo zypper install libwebkit2gtk-4_1-0"},
		{"ID=nixos\n", "WebKitGTK 4.1"},
		{"", "WebKitGTK 4.1"},
	} {
		if got := installHint(nil, c.release); !strings.Contains(got, c.want) {
			t.Errorf("%q → %q，應含 %q", c.release, got, c.want)
		}
	}
	// 不誤判：ID 剛好是別的字的一部分。
	if got := installHint(nil, "ID=archive\n"); strings.Contains(got, "pacman") {
		t.Errorf("%s", got)
	}
}

// 應用程式選單的項目只在有可啟動的設定程式時存在，且指向穩定的 0c-update。
func TestSettingsEntryFollowsWhetherThereIsAProgramToStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	entry := filepath.Join(home, ".local/share/applications", settingsapp.DesktopFile)
	stubIconsChanged(t)

	ensureSettingsEntry() // 沒有設定程式：不建立
	if _, err := os.Stat(entry); err == nil {
		t.Fatal("沒有設定程式時不該有選單項目")
	}

	// 完整安裝的設定程式（收據記錄雜湊）與穩定的 0c-update。
	bin := filepath.Join(home, ".local/bin")
	os.MkdirAll(bin, 0o700)
	program := filepath.Join(bin, "0c-settings")
	os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700)
	sum, _ := update.Hash(program)
	base := basePath()
	os.MkdirAll(filepath.Join(base, "bin"), 0o700)
	os.WriteFile(stableUpdater(base), []byte("u"), 0o700)
	os.WriteFile(receiptPath(), []byte(`{"version":"0.1.0-dev.43","files":{".local/bin/0c-settings":"`+sum+`"},"active":true}`), 0o600)

	ensureSettingsEntry()
	b, err := os.ReadFile(entry)
	if err != nil || !strings.Contains(string(b), `Exec="`+stableUpdater(base)+`" open-settings`) {
		t.Fatalf("%v：%s", err, b)
	}

	// 程式被替換（雜湊不符）：選單項目一併移除，不留開不起來的入口。
	os.WriteFile(program, []byte("#!/bin/sh\nexit 1\n"), 0o700)
	ensureSettingsEntry()
	if _, err := os.Stat(entry); err == nil {
		t.Fatal("核對不過時應移除選單項目")
	}

	// 解除安裝移除本程式建立的項目。
	os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700)
	ensureSettingsEntry()
	removeSettingsEntry()
	if _, err := os.Stat(entry); err == nil {
		t.Fatal("解除安裝後不該留下選單項目")
	}
}

// #276：使用者直接執行 install.py install 時不經過 afterInstall；install.py 裝好後呼叫 after-install，
// 做一行安裝裝完後的同一組步驟（排程、選單項目）。
func TestAfterInstallCommandRunsTheInstallSteps(t *testing.T) {
	saved := directAfterInstall
	t.Cleanup(func() { directAfterInstall = saved })
	n := 0
	directAfterInstall = func() { n++ }
	if err := run([]string{"after-install", "extra"}); err == nil || n != 0 {
		t.Fatalf("after-install 不接受參數：%v，執行了 %d 次", err, n)
	}
	if err := run([]string{"after-install"}); err != nil || n != 1 {
		t.Fatalf("%v：執行了 %d 次", err, n)
	}
}

// 直接安裝時還沒有穩定副本（排程建不起來時也一樣）：選單項目指向 ~/.local/bin/0c-update。
func TestSettingsEntryForADirectInstallPointsAtTheInstalledUpdater(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	entry := filepath.Join(home, ".local/share/applications", settingsapp.DesktopFile)
	bin := filepath.Join(home, ".local/bin")
	os.MkdirAll(bin, 0o700)
	program := filepath.Join(bin, "0c-settings")
	os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700)
	updater := filepath.Join(bin, "0c-update")
	os.WriteFile(updater, []byte("u"), 0o700)
	sum, _ := update.Hash(program)
	os.MkdirAll(basePath(), 0o700)
	os.WriteFile(receiptPath(), []byte(`{"version":"0.2.10","files":{".local/bin/0c-settings":"`+sum+`"},"active":true}`), 0o600)

	notified := stubIconsChanged(t)
	ensureSettingsEntry()
	b, err := os.ReadFile(entry)
	if err != nil || !strings.Contains(string(b), `Exec="`+updater+`" open-settings`) {
		t.Fatalf("%v：%s", err, b)
	}
	// 寫入了選單項目與圖示：請 KDE 重新掃描圖示，啟動器才不是空白圖示；沒有變動時不必再通知。
	if *notified != 1 {
		t.Fatalf("寫入後通知 %d 次", *notified)
	}
	if ensureSettingsEntry(); *notified != 1 {
		t.Fatalf("沒有變動卻通知了 %d 次", *notified)
	}
}

// stubIconsChanged 不對執行測試的桌面送出訊號，只計算次數。
func stubIconsChanged(t *testing.T) *int {
	saved := iconsChanged
	t.Cleanup(func() { iconsChanged = saved })
	n := 0
	iconsChanged = func() { n++ }
	return &n
}
