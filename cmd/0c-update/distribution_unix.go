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

// delegateToPersonal：執行的是系統安裝的 0c-update，而這個帳號另外有官網安裝時，整個命令交給官網安裝的
// 0c-update（成功時不會返回）：外殼、引擎、設定程式用的都是官網那一份，status、set、開設定程式（例如 IBus
// 輸入來源的齒輪）都要對它做，不然會把系統套件的版本與「由套件管理員更新」報成使用中的安裝。
func delegateToPersonal(args []string) bool {
	self, err := os.Executable()
	if err != nil || !installs.Default().InSystem(self) {
		return false
	}
	p, ok := installs.Default().Personal()
	if !ok || p.Updater == "" {
		return false
	}
	return syscall.Exec(p.Updater, append([]string{p.Updater}, args...), os.Environ()) == nil
}
