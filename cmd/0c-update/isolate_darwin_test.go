package main

import "fmt"

func isolateResidentEngine() func() {
	spawnResident = func() error { return nil }
	adoptSchedule = func(bool) {} // 不改這台機器的背景項目登記與備份（#205）
	// 安裝、回復與排程的測試不結束、不啟動這台機器上的輸入法程式，也不看這台機器有沒有鎖定。
	list, observe, away, launch := listMacShells, observeMacShells, userAway, launchMacShell
	listMacShells = func(string) []uiProcess { return nil }
	observeMacShells = func(string) ([]uiProcess, error) { return nil, nil }
	userAway = func() bool { return false }
	launchMacShell = func(string) {}
	state, relogin, retire, spawn, kick, printAgent := agentScheduleState, agentReloginRequired, retireForAdoption, spawnAdopt, kickAgent, launchctlPrintAgent
	agentScheduleState = func(string, string) (string, error) { return "", fmt.Errorf("isolated agent state") }
	agentReloginRequired = func(string) bool { return true }
	retireForAdoption = func(string) bool { return false }
	spawnAdopt, kickAgent = func() {}, func() {}
	launchctlPrintAgent = func() (string, error) { return "", fmt.Errorf("isolated launchctl") }
	return func() {
		listMacShells, observeMacShells, userAway, launchMacShell = list, observe, away, launch
		agentScheduleState, agentReloginRequired, retireForAdoption, spawnAdopt, kickAgent, launchctlPrintAgent = state, relogin, retire, spawn, kick, printAgent
	}
}
