//go:build !windows

package main

import (
	"os"
	"syscall"
)

// reexec 以同樣的參數與環境換成新的執行檔（#181 的升級交接）。行程編號、父行程與 cgroup 都不變：
// 由外殼帶起的引擎仍是外殼的子行程，由 systemd 使用者單元帶起的仍屬於同一個單元。
// 個人資料鎖與端點已在交接時放開；Go 開的檔案都帶 close-on-exec，不會帶進新程式。
func reexec(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
