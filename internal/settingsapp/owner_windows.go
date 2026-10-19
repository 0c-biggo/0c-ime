//go:build windows

package settingsapp

import "io/fs"

func ownedByCurrentUser(fs.FileInfo) bool { return true }
