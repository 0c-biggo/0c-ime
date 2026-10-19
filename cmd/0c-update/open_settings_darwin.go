//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/settingsapp"
)

func newSettingsOpener() settingsOpener {
	updater, _ := os.Executable()
	return settingsOpener{
		locate: func() (settingsapp.Location, error) {
			app, files := settingsAppFromReceipt(receiptPath(), paths().InputMethods)
			if app == "" {
				return settingsapp.Location{}, settingsapp.ErrNotFound
			}
			return settingsapp.LocateBundle(app, files)
		},
		display: func() bool { return true }, // 從輸入法選單啟動時一定在圖形工作階段裡
		missing: func(string) []string { return nil },
		launch:  func(path string, o settingsapp.LaunchOptions) error { return settingsapp.LaunchApp(path, o) },
		notify: func(title, body string) {
			if !stderrIsTerminal() { // 在終端機執行時，錯誤已經印在眼前
				_ = notify(autoupdate.Notice{Title: title, Body: body})
			}
		},
		entry:   ensureSettingsEntry,
		hint:    func([]string) string { return "" },
		updater: updater,
		wait:    15 * time.Second,
	}
}

func stderrIsTerminal() bool {
	st, err := os.Stderr.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func ensureSettingsEntry() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	changed, err := syncMacSettingsEntry(filepath.Join(home, "Applications"), receiptPath(), paths().InputMethods, stableUpdater(basePath()))
	if err == nil && changed {
		// 執行收據核對過的內附程式登記副本；不靠 private lsregister 工具，也不開 Finder。
		r, readErr := readMacSettingsReceipt(receiptPath())
		if readErr != nil {
			err = readErr
		} else {
			program := filepath.Join(paths().InputMethods, r.App, settingsLauncherPrefix, "Contents/MacOS", settingsapp.MacLauncherExecutable)
			err = exec.Command(program, "--register-entry", filepath.Join(home, "Applications", settingsapp.MacLauncherName)).Run()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 設定入口未建立：", err)
	}
}

func removeSettingsEntry() {
	home, err := os.UserHomeDir()
	if err == nil {
		err = settingsapp.RemoveMacLauncher(filepath.Join(home, "Applications"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 設定入口未移除：", err)
	}
}
