package main

import (
	"fmt"
	"time"
)

func isolateResidentEngine() func() {
	spawnResident = func() error { return nil }
	adoptSchedule = func(bool) {} // 不改這台機器的背景項目登記與備份（#205）
	// 安裝、回復與排程的測試不結束、不啟動這台機器上的輸入法程式，也不看這台機器有沒有鎖定。
	list, observe, away, launch := listMacShells, observeMacShells, userAway, launchMacShell
	listMacShells = func(string) []uiProcess { return nil }
	observeMacShells = func(string) ([]uiProcess, error) { return nil, nil }
	userAway = func() bool { return false }
	launchMacShell = func(string) {}
	checkInputSource := checkInputSourceAfterHandover
	checkInputSourceAfterHandover = func(string) {} // 不查、不重新登記這台機器的輸入來源（#351）
	followUp, spawnFollowUp := followUpInputSource, spawnInputSourceFollowUp
	followUpInputSource = func(string, string, string, time.Duration) bool { return false } // 不起補做的背景程式（#348）
	spawnInputSourceFollowUp = func() {}
	legacyMode := completeLegacyInputMode
	completeLegacyInputMode = func(string) {} // 不對這台機器的輸入來源送要求（#348）
	state, relogin, retire, spawn, kick, printAgent := agentScheduleState, agentReloginRequired, retireForAdoption, spawnAdopt, kickAgent, launchctlPrintAgent
	agentScheduleState = func(string, string) (string, error) { return "", fmt.Errorf("isolated agent state") }
	agentReloginRequired = func(string) bool { return true }
	retireForAdoption = func(string) bool { return false }
	spawnAdopt, kickAgent = func() {}, func() {}
	launchctlPrintAgent = func() (string, error) { return "", fmt.Errorf("isolated launchctl") }
	return func() {
		listMacShells, observeMacShells, userAway, launchMacShell = list, observe, away, launch
		checkInputSourceAfterHandover = checkInputSource
		followUpInputSource, spawnInputSourceFollowUp = followUp, spawnFollowUp
		completeLegacyInputMode = legacyMode
		agentScheduleState, agentReloginRequired, retireForAdoption, spawnAdopt, kickAgent, launchctlPrintAgent = state, relogin, retire, spawn, kick, printAgent
	}
}
