//go:build windows

package settingsapp

import "io/fs"

func ownedByCurrentUser(fs.FileInfo) bool { return true }

var systemOwner = 0

func ownedBy(fs.FileInfo, int) bool { return true }
