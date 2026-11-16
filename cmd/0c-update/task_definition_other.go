//go:build !windows

package main

// refreshTaskDefinitions：只有 Windows 的排程由提權的安裝程式建立、要一般權限換新（見 auto_windows.go）。
func refreshTaskDefinitions(string) {}
