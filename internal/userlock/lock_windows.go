//go:build windows

package userlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// 鎖定檔案尾端以外的一個位元組（超過檔尾的範圍可以鎖），開頭記錄的 pid 仍讀得到。
// 鎖綁在這個 handle 上：同一行程另外開啟也會互斥，行程結束時由系統放開。Go 開的 handle 不可繼承。
const lockOffsetHigh = 0x7fffffff

func lockFile(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: lockOffsetHigh}
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}

func unlockFile(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: lockOffsetHigh}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
