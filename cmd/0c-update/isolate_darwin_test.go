package main

func isolateResidentEngine() func() {
	spawnResident = func() error { return nil }
	// 安裝、回復與排程的測試不結束這台機器上執行中的輸入法程式（shell_handover_darwin_test.go 自己換回來）。
	list := listMacShells
	listMacShells = func(string) []uiProcess { return nil }
	return func() { listMacShells = list }
}
