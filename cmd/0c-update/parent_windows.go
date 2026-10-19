package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// parentWatch：Windows 不會改父行程編號，所以啟動時先開父行程的 handle（拿著 handle，編號就不會被重用），
// 之後查它是否已經結束。開不了（父行程已經不在、權限不足）時當成父行程仍在，照舊靠寫入失敗結束。
func parentWatch() func() bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(os.Getppid()))
	if err != nil {
		return func() bool { return false }
	}
	return func() bool {
		ev, err := windows.WaitForSingleObject(h, 0)
		return err == nil && ev == windows.WAIT_OBJECT_0
	}
}
