//go:build !windows

package settingsapp

import (
	"io/fs"
	"os"
	"syscall"
)

func ownedByCurrentUser(st fs.FileInfo) bool { return ownedBy(st, os.Getuid()) }

// systemOwner 是系統套件檔案的擁有者（root）；測試換成目前使用者。
var systemOwner = 0

func ownedBy(st fs.FileInfo, uid int) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == uid
}
