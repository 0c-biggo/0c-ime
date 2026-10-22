//go:build !darwin

package main

// adoptSchedule：只有 macOS 的 SMAppService 排程會跟到備份裡的 App（#205）。
var adoptSchedule = func(foreground bool) {}
