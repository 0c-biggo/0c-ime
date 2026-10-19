//go:build !windows

package settingsapp

import (
	"io/fs"
	"os"
	"syscall"
)

func ownedByCurrentUser(st fs.FileInfo) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(sys.Uid) == os.Getuid()
}
