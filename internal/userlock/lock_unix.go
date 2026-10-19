//go:build !windows

package userlock

import (
	"errors"
	"os"
	"syscall"
)

// flock 綁在開啟的檔案描述上：同一行程另外開啟同一個檔案也會互斥，行程結束時由核心放開。
// Go 開檔一律帶 O_CLOEXEC，神經 worker 等子行程不會繼承這把鎖。
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrBusy
		}
		return err
	}
}

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
