//go:build !windows

package main

// refreshStableUpdater：Linux 由套件的 install.py 換（見 scripts/install_package.py），macOS 的 installSchedule
// 本來就用新套件內附的 0c-update；只有 Windows 的 ZIP 自動更新沒有新套件自帶的安裝腳本。
func refreshStableUpdater(string) (bool, error) { return false, nil }
