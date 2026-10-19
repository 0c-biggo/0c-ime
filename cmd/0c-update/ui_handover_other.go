//go:build !windows

package main

import "time"

// 候選窗程式的交接只有 Windows 需要：其他平台的外殼在輸入法框架裡，更新時由外殼更新流程重新載入。
func handOverUI(time.Duration) {}
func stopUI(time.Duration)     {}

const (
	uiWaitInstall = 0
	uiWaitTask    = 0
	uiWaitAuto    = 0
)
