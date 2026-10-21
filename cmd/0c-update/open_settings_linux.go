//go:build !windows && !darwin

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/installs"
	"github.com/0c-biggo/0c-ime/internal/settingsapp"
)

// 完整安裝放設定程式的位置（相對家目錄，也是安裝收據記錄雜湊的鍵）。
const installedSettings = ".local/bin/0c-settings"

func newSettingsOpener() settingsOpener {
	base := basePath()
	home, _ := os.UserHomeDir()
	updater, _ := os.Executable()
	entry := ensureSettingsEntry
	locate := func() (settingsapp.Location, error) {
		return settingsapp.Locate(base, filepath.Join(home, installedSettings), receiptHash(installedSettings))
	}
	if packageManaged() {
		// 系統套件（#181）：設定程式是 <bin>/0c-settings，應用程式選單的項目由套件放在系統目錄，不另外寫到家目錄。
		entry = func() {}
		locate = func() (settingsapp.Location, error) {
			return settingsapp.LocateSystem(filepath.Join(installs.Default().Bin, "0c-settings"))
		}
	}
	return settingsOpener{
		locate:  locate,
		display: settingsapp.DisplayAvailable,
		missing: settingsapp.MissingLibraries,
		launch:  settingsapp.Launch,
		notify: func(title, body string) {
			if !stderrIsTerminal() { // 在終端機執行時，錯誤已經印在眼前
				_ = notify(autoupdate.Notice{Title: title, Body: body})
			}
		},
		entry:   entry,
		hint:    func(libs []string) string { return installHint(libs, readOSRelease()) },
		updater: updater,
		log:     filepath.Join(base, "settings-last.log"),
		wait:    1500 * time.Millisecond,
	}
}

func stderrIsTerminal() bool {
	st, err := os.Stderr.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// receiptHash 回傳完整安裝收據記錄的檔案雜湊（鍵是相對家目錄的路徑）；沒有時為空字串。
func receiptHash(relative string) string {
	b, err := os.ReadFile(receiptPath())
	if err != nil {
		return ""
	}
	var receipt struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(b, &receipt) != nil {
		return ""
	}
	return receipt.Files[relative]
}

// ensureSettingsEntry 讓應用程式選單的「靈犀注音設定」項目與這個安裝一致：有可啟動的設定程式才建立，
// 沒有就移除（舊套件升級後不留下開不起來的項目）。項目指向穩定的 0c-update，不指向某個版本的檔案。
func ensureSettingsEntry() {
	base := basePath()
	home, _ := os.UserHomeDir()
	if _, err := settingsapp.Locate(base, filepath.Join(home, installedSettings), receiptHash(installedSettings)); err != nil {
		_ = settingsapp.RemoveDesktopEntry(settingsapp.DataHome())
		return
	}
	for _, updater := range []string{stableUpdater(base), filepath.Join(home, ".local/bin/0c-update")} {
		if st, err := os.Stat(updater); err == nil && st.Mode().IsRegular() {
			_, _ = settingsapp.EnsureDesktopEntry(settingsapp.DataHome(), updater)
			return
		}
	}
}

func removeSettingsEntry() { _ = settingsapp.RemoveDesktopEntry(settingsapp.DataHome()) }

func readOSRelease() string {
	b, _ := os.ReadFile("/etc/os-release")
	return string(b)
}

// installHint 依發行版回傳安裝 WebKitGTK 的指令；認不得的發行版只說明要裝什麼。
func installHint(missing []string, osRelease string) string {
	fields := map[string]string{}
	for _, line := range strings.Split(osRelease, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	family := " " + fields["ID"] + " " + fields["ID_LIKE"] + " "
	has := func(names ...string) bool {
		for _, n := range names {
			if strings.Contains(family, " "+n+" ") {
				return true
			}
		}
		return false
	}
	switch {
	case has("debian", "ubuntu"):
		return "請安裝：sudo apt install libwebkit2gtk-4.1-0。"
	case has("fedora", "rhel", "centos"):
		return "請安裝：sudo dnf install webkit2gtk4.1。"
	case has("arch", "manjaro"):
		return "請安裝：sudo pacman -S webkit2gtk-4.1。"
	case has("suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed"):
		return "請安裝：sudo zypper install libwebkit2gtk-4_1-0。"
	}
	return "請安裝 WebKitGTK 4.1（libwebkit2gtk-4.1）。"
}
