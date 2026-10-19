//go:build !darwin && !windows

package main

// shellDiagnostics：Linux 的外殼位置固定，更新器已能核對。
func shellDiagnostics() map[string]any { return nil }
