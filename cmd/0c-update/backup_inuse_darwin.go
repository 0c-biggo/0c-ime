//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// macBackupInUse 給 update.MacPaths.InUse：第一次詢問時才觀察系統（lsof、launchctl），同一次清理共用結果。
func macBackupInUse() func(dir string) bool {
	var once sync.Once
	var usage backupUsage
	return func(dir string) bool {
		once.Do(func() { usage = observeBackupUsage() })
		return usage.inUse(dir, appBundleVersion)
	}
}

// observeBackupUsage 列出這個帳號的程序從哪裡執行與背景項目的登記；說明見 backup_inuse_mac.go。
var observeBackupUsage = func() backupUsage {
	uid := strconv.Itoa(os.Getuid())
	var u backupUsage
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-n", "-P", "-w", "-u", uid, "-F", "fn").Output()
	if err != nil || len(out) == 0 {
		return backupUsage{unknown: true}
	}
	u.open = lsofPaths(string(out))
	if self, err := os.Executable(); err == nil {
		u.open = append(u.open, self)
		if real, err := filepath.EvalSymlinks(self); err == nil {
			u.open = append(u.open, real)
		}
	}
	out, err = exec.CommandContext(ctx, "/bin/launchctl", "print", "gui/"+uid+"/"+agentLabel).CombinedOutput()
	switch {
	case err == nil:
		u.agent = agentBundleVersion(string(out))
	case !agentNotRegistered(string(out)):
		return backupUsage{unknown: true}
	}
	return u
}
