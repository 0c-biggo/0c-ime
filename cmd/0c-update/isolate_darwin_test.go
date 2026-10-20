package main

func isolateResidentEngine() func() {
	spawnResident = func() error { return nil }
	// 安裝、回復與排程的測試不結束、不啟動這台機器上的輸入法程式，也不看這台機器有沒有鎖定。
	list, away, launch := listMacShells, userAway, launchMacShell
	listMacShells = func(string) []uiProcess { return nil }
	userAway = func() bool { return false }
	launchMacShell = func(string) {}
	return func() { listMacShells, userAway, launchMacShell = list, away, launch }
}
