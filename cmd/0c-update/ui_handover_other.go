//go:build !windows && !darwin

package main

import "time"

// 輸入法程式的交接只有 Windows（0c-ui）與 macOS（0c.app）需要：Linux 的外殼在輸入法框架裡，更新時由外殼更新流程重新載入。
func handOverUI(time.Duration) {}
func stopUI(time.Duration)     {}

const (
	uiWaitInstall = 0
	uiWaitTask    = 0
	uiWaitAuto    = 0
)

// shellPendingAfterInstall：Linux 的外殼由 installReloadableShell 重載，不經這裡。
func shellPendingAfterInstall() bool { return true }
