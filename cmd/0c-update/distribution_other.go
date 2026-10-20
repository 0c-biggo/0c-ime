//go:build windows || darwin

package main

import "github.com/0c-biggo/0c-ime/internal/installs"

// 系統套件安裝（#181）只有 Linux；macOS 與 Windows 一律是官網安裝。
func packageManagedExe(string) bool           { return false }
func installList() []installs.Install         { return nil }
func distributionVersion() string             { return "" }
func delegateOpenSettings(args []string) bool { return false }
