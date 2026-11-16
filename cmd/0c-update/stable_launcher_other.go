//go:build !windows

package main

// refreshStableLauncher：只有 Windows 的排程需要不開主控台的啟動器（systemd、launchd 本來就不開視窗）。
func refreshStableLauncher(string) {}
