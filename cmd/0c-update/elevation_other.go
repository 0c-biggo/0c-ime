//go:build !windows

package main

import "fmt"

func validateElevationOrigin(_, sid, base string) error {
	if sid != "" || base != "" {
		return fmt.Errorf("此平台不支援 UAC 更新交接")
	}
	return nil
}

// 其他平台的外殼更新不需要 UAC（shellUnattended 已是 true），這兩個判斷用不到。
func shellKeepsRegistration(_, _ string) bool     { return false }
func pendingShellKeepsRegistration(_ string) bool { return false }

// validateHandoverOrigin：只有 Windows 的提權交接會帶原使用者。
func validateHandoverOrigin(sid, base string) error {
	return validateElevationOrigin("finish", sid, base)
}
