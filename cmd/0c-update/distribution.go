package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

// 「由系統套件管理員更新」模式（#181）：發行版以 make install 把 0c-update 放在 libexec/0c，並放上
// share/0c/distribution.json（updates=package-manager）。這時更新、安裝、回復、排程都交給套件管理員，
// 0c-update 不連網、不建排程、不複製穩定副本；設定、詞彙、本機 API、開啟設定程式照常。

// errPackageManaged：在這個模式下被拒絕的命令。設定程式依 code 判斷（契約 package_managed）。
var errPackageManaged = errors.New("這份靈犀注音由系統套件管理員安裝與更新，請用套件管理員（例如 apt）更新或移除")

// packageModeCommands 是套件管理員模式下照常可用的命令（models：#180 的模型下載，Debian 套件不帶模型；
// profile：輸入習慣，同 set 只改使用者的設定，#229）；其他（check、download、download-now、apply、install、
// verify、rollback、uninstall、schedule、auto、finish、mode、pause、resume、snooze、notify-action、protocol）都拒絕。
var packageModeCommands = map[string]bool{
	"status": true, "settings": true, "profile": true, "set": true, "models": true, "vocab": true, "api": true, "engine": true, "open-settings": true,
}

// packageManagedCheck 測試替換用。
var packageManagedCheck = func() bool {
	exe, err := os.Executable()
	return err == nil && packageManagedExe(exe)
}

// packageManaged 回報正在執行的 0c-update 是否屬於由套件管理員更新的系統安裝。
func packageManaged() bool { return packageManagedCheck() }

// refusePackageManaged：套件管理員模式下不能執行的命令回傳 errPackageManaged；帶 -json 時以契約格式輸出。
func refusePackageManaged(action string, args []string) error {
	if packageModeCommands[action] || !packageManaged() {
		return nil
	}
	err := fmt.Errorf("%s：%w", action, errPackageManaged)
	for _, a := range args {
		if isJSONFlag(a) {
			return failJSON(os.Stdout, err)
		}
	}
	return err
}

// updatesMode 是 status 回報的 updates：self（官網安裝，自己更新）或 package-manager。
func updatesMode() string {
	if packageManaged() {
		return installs.UpdatesPackageManager
	}
	return installs.UpdatesSelf
}

// printInstalls 在 status 列出找到的安裝；只有一份官網安裝（一般情況）時不列，輸出跟以前一樣。
func printInstalls(w io.Writer, list []installs.Install) {
	if len(list) == 0 || (len(list) == 1 && list[0].Kind == installs.KindPersonal) {
		return
	}
	for _, in := range list {
		name := "官網安裝"
		if in.Kind == installs.KindSystem {
			name = "系統套件"
		}
		state := "（未使用）"
		if in.Active {
			state = "（使用中）"
		}
		fmt.Fprintf(w, "%s%s：引擎 %s；字典 %s\n", name, state, in.Server, in.Data)
	}
}
