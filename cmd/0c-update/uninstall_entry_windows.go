//go:build windows

package main

import "golang.org/x/sys/windows/registry"

// uninstallEntry 是安裝程式（setup.exe）在「已安裝的應用程式」登記的項目，見 platforms/windows/installer/setup.nsi。
const uninstallEntry = `Software\Microsoft\Windows\CurrentVersion\Uninstall\0c-Zhuyin`

// syncUninstallEntry 讓「已安裝的應用程式」顯示實際安裝的版本：自動更新與回復不經過安裝程式，
// 原本會一直停在當初用安裝程式裝的版本（#82）。沒有用安裝程式安裝（一行安裝）時沒有這個項目，不建立。
func syncUninstallEntry() {
	version, err := installedVersion()
	if err != nil || version == "" {
		return
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallEntry, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	if current, _, err := key.GetStringValue("DisplayVersion"); err == nil && current == version {
		return
	}
	_ = key.SetStringValue("DisplayVersion", version)
}
