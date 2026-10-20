//go:build windows

package main

import (
	"errors"
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

// ensureSettingsEntry 讓開始功能表的「靈犀注音設定」與這個安裝一致：有可啟動的設定程式才建立，確定沒有
// 才移除（設定程式被改過時留著，點下去才看得到「無法通過核對」的說明）。項目指向穩定的 0c-update，
// 不指向某個版本的檔案。
func ensureSettingsEntry() {
	if os.Getenv("ZC_NO_SCHEDULE") != "" { // 測試環境不碰真實的開始功能表
		return
	}
	base := basePath()
	installed, hash := installedFromReceipt(receiptPath(), base, enginedir.SettingsExecutable())
	if _, err := settingsapp.Locate(base, installed, hash); errors.Is(err, settingsapp.ErrNotFound) {
		_ = settingsapp.RemoveShortcut(base)
		return
	} else if err != nil {
		return
	}
	stable := stableUpdater(base)
	if st, err := os.Stat(stable); err != nil || !st.Mode().IsRegular() {
		return
	}
	_, _ = settingsapp.EnsureStartMenuShortcut(base, stable)
}

func removeSettingsEntry() { _ = settingsapp.RemoveShortcut(basePath()) }
