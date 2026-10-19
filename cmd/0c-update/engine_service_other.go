//go:build !linux && !windows && !darwin

package main

// 其他平台沒有引擎服務（#120）：一切照舊。
func ensureEngineService(string)           {}
func removeEngineService()                 {}
func startEngineService() bool             { return false }
func engineServiceState() (string, string) { return "absent", "" }
func drainEngineForInstaller()             {}
func engineAfterInstall(string)            {}
