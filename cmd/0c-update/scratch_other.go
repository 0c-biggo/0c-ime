//go:build !linux

package main

// scratchRoot：Windows 與 macOS 的系統暫存目錄本來就在使用者的磁碟上；Windows 還有路徑長度限制，不移動。
func scratchRoot() string { return "" }
