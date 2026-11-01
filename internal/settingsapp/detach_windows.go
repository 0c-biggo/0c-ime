//go:build windows

package settingsapp

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// 脫離呼叫端的主控台與行程群組：從選單（0c-ui）或捷徑啟動時，呼叫端結束不會連帶影響設定程式。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}

var procAllowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// grantForeground 把前景權限交給剛啟動的設定程式（#251）：權限不會從這個主控台程式自動傳給它，選單按下時交下來的
// （見 TSF 的 langbar）要在這裡明確傳下去。呼叫端沒有權限時系統會拒絕，視窗照舊開在後面，所以結果不必處理。
var grantForeground = func(pid int) { _, _, _ = procAllowSetForegroundWindow.Call(uintptr(pid)) }
