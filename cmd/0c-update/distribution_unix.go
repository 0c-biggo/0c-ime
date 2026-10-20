//go:build !windows && !darwin

package main

import (
	"os"
	"syscall"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

func packageManagedExe(exe string) bool { return installs.Default().PackageManaged(exe) }

// installList 是 status 的 installs：找到的官網安裝與系統安裝，外殼目前用的那一份 active（官網安裝優先）。
func installList() []installs.Install { return installs.Default().All() }

// distributionVersion 是系統安裝的 distribution.json 記的版本（套件管理員模式的 installed）。
func distributionVersion() string {
	d, _ := installs.Default().Distribution()
	return d.Version
}

// delegateOpenSettings：系統安裝的 0c-update 被叫去開設定程式（例如 IBus 輸入來源的齒輪），而這個帳號另外有
// 官網安裝時，交給官網安裝的 0c-update：外殼用的是官網那一份，設定程式也要讀它的狀態。
func delegateOpenSettings(args []string) bool {
	self, err := os.Executable()
	if err != nil || !installs.Default().InSystem(self) {
		return false
	}
	p, ok := installs.Default().Personal()
	if !ok || p.Updater == "" {
		return false
	}
	return syscall.Exec(p.Updater, append([]string{p.Updater, "open-settings"}, args...), os.Environ()) == nil
}
