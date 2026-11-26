//go:build !linux && !windows

package main

// scratchRoot：macOS 的系統暫存目錄（$TMPDIR）本來就是每位使用者自己的目錄。
func scratchRoot() string { return "" }
