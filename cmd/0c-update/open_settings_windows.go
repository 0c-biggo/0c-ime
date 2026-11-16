//go:build windows

package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/settingsapp"
	"golang.org/x/sys/windows"
)

func newSettingsOpener() settingsOpener {
	base := basePath()
	updater, _ := os.Executable()
	return settingsOpener{
		locate: func() (settingsapp.Location, error) {
			installed, hash := installedFromReceipt(receiptPath(), base, enginedir.SettingsExecutable())
			return settingsapp.Locate(base, installed, hash)
		},
		display: settingsapp.DisplayAvailable,
		missing: settingsapp.MissingLibraries,
		launch:  settingsapp.Launch,
		notify: func(title, body string) {
			if !consoleAttached() { // 從選單（0c-ui）或捷徑啟動時沒有主控台，錯誤要以通知說明
				_ = notify(autoupdate.Notice{Title: title, Body: body})
			}
		},
		entry:   ensureSettingsEntry,
		hint:    func([]string) string { return settingsapp.WebView2Hint },
		updater: updater,
		log:     filepath.Join(base, "settings-last.log"),
		wait:    1500 * time.Millisecond,
	}
}

// consoleAttached 是 stderr 真的接在主控台上（而不是 NUL 或管道）。NUL 在 Windows 上也是字元裝置，
// 所以不能用檔案模式判斷。
func consoleAttached() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stderr.Fd()), &mode) == nil
}

// ensureSettingsEntry 讓開始功能表的「靈犀注音設定（0c）」與這個安裝一致：有可啟動的設定程式才建立，確定沒有
// 才移除（設定程式被改過時留著）。捷徑指向 bin\ 裡設定程式的穩定副本，不指向某個版本的檔案，也不經 conhost；
// 規則見 start_menu_entry.go（#284）。
func ensureSettingsEntry() { windowsSettingsEntry(false) }

// restoreSettingsEntry：完整安裝之後，使用者或防毒軟體移除過的捷徑也補回。
func restoreSettingsEntry() { windowsSettingsEntry(true) }

func windowsSettingsEntry(restore bool) {
	if os.Getenv("ZC_NO_SCHEDULE") != "" { // 測試環境不碰真實的開始功能表
		return
	}
	base := basePath()
	startMenuEntry{
		base:      base,
		restore:   restore,
		installed: installedVersion,
		locate:    func() (string, string, error) { return currentSettings(base, receiptPath()) },
		spec:      func(stable string) (settingsapp.ShortcutSpec, error) { return settingsapp.StartMenuSpec(base, stable) },
		ensure: func(spec settingsapp.ShortcutSpec, restore bool) (bool, error) {
			return settingsapp.EnsureStartMenuShortcut(base, spec, restore)
		},
	}.sync()
}

// removeSettingsEntry（解除安裝）：移除捷徑、標記檔與設定程式的穩定副本。
func removeSettingsEntry() { startMenuEntry{base: basePath()}.remove() }
