//go:build windows

package neural

import (
	"os/exec"
	"syscall"
)

// 引擎本身沒有主控台；不加這個旗標，每次啟動 worker 都會跳出一個主控台視窗。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
