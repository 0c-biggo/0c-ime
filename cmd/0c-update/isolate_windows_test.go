package main

func isolateResidentEngine() func() {
	startResident = func(string) error { return nil }
	// 安裝、排程與登入工作的測試不碰這台機器上執行中的 0c-ui（ui_handover_windows_test.go 自己換回來）。
	control := uiControlFor
	uiControlFor = func() uiControl {
		c := control()
		c.list = func() []uiProcess { return nil }
		return c
	}
	return func() { uiControlFor = control }
}
