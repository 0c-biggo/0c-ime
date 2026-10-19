//go:build !windows

package main

import "fmt"

// 只有 Windows 需要登入時先啟動常駐引擎（沙箱應用程式裡的 DLL 不能自己啟動）；
// 其他平台由輸入法框架或外殼的下一個按鍵啟動引擎。
func ensureEngine() error     { return fmt.Errorf("此平台由輸入法框架啟動引擎") }
func ensureEngineTask(string) {}
func removeResidentEngine()   {}

// restartResidentEngine：套用設定時引擎已交接退出；立刻叫起常駐引擎（#120），
// 設定程式與本機 API 不必等下一個按鍵。
func restartResidentEngine() { ensureEngineService(basePath()) }

// 固定路徑的 TSF DLL（#121）只有 Windows 有。
func repairFixedDLL(string) {}
func deployTSF() error      { return fmt.Errorf("deploy-tsf 只用於 Windows") }
