//go:build !windows

package settingsapp

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// 只有 Windows 有前景鎖（#251）；其他平台的視窗管理不靠這個權限。
var grantForeground = func(int) {}
