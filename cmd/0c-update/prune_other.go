//go:build !windows

package main

import "fmt"

// macOS、Linux 在安裝時同步清理（internal/update 的 pruneMacBackups、install.py），沒有背景清理行程。
func spawnPrune(string) error { return nil }
func pruneExecutable() string { return "" }
func pruneInstallsNow(string) {}
func pruneCommand() error     { return fmt.Errorf("prune 只用於 Windows") }
