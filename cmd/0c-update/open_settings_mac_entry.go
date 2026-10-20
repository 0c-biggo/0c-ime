//go:build !windows

package main

import (
	"errors"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
)

// syncMacSettingsEntry 的路徑由呼叫端提供，安裝／回復測試不需碰真的家目錄或 LaunchServices。
// 舊版沒有內附啟動器時移除入口；驗證錯誤則保留，讓開啟時的既有核對流程說明失敗原因。
func syncMacSettingsEntry(applications, receipt, inputMethods, stable string) (bool, error) {
	r, err := readMacSettingsReceipt(receipt)
	if errors.Is(err, settingsapp.ErrNotFound) {
		return false, settingsapp.RemoveMacLauncher(applications)
	}
	if err != nil {
		return false, err
	}
	root := filepath.Join(inputMethods, r.App)
	settingsFiles := settingsapp.BundleFiles(r.Files, settingsAppPrefix)
	launcherFiles := settingsapp.BundleFiles(r.Files, settingsLauncherPrefix)
	if len(settingsFiles) == 0 || len(launcherFiles) == 0 {
		return false, settingsapp.RemoveMacLauncher(applications)
	}
	if _, err := settingsapp.LocateBundle(filepath.Join(root, settingsAppPrefix), settingsFiles); err != nil {
		return false, err
	}
	source := filepath.Join(root, settingsLauncherPrefix)
	if err := settingsapp.VerifyBundle(source, launcherFiles); err != nil {
		return false, err
	}
	// 排程關閉或建立失敗仍要有開得起來的設定入口；只換目前收據的更新器，不重建背景排程。
	updater := filepath.Join(root, "Contents/MacOS/0c-update")
	want := r.Files["Contents/MacOS/0c-update"]
	if err := settingsapp.EnsureMacUpdater(updater, want, stable); err != nil {
		return false, err
	}
	return settingsapp.EnsureMacLauncher(applications, source, launcherFiles)
}
