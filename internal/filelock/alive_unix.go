//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// ProcessAlive 回報 pid 的行程是否仍在執行。
func ProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
