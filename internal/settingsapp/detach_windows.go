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
