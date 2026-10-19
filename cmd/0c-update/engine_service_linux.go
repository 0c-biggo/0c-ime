package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

// 引擎服務（#120，Linux）。三個平台一致：不新增自動啟動項目，沿用既有的——Linux 是 0c-update-login.service
// （登入）與 0c-update.timer（每 15 分鐘）。0.2.0 起每次喚起若引擎沒在跑，就以 systemd-run 暫時性
// service 啟動（startEngine：行程由 user manager 產生，不繼承排程的 Nice=10 與 idle I/O，#141）。
// 引擎常駐，讓設定程式與本機 API 在沒打字時也連得到；當掉後由外殼的下一個按鍵或下一次喚起叫起
// （同 Windows 的登入工作、macOS 的更新 agent）。不留任何 unit 檔，安裝、回復、解除安裝都不必另外處理。

// engineRunning 測試替換用。
var engineRunning = func() bool {
	c, err := dialEngine(time.Second)
	if err == nil {
		c.Close()
	}
	return err == nil
}

func ensureEngineService(base string) {
	if engineRunning() {
		return
	}
	if err := startEngine(base); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 引擎沒有啟動（外殼會在下一個按鍵自己啟動）：", err)
	}
}

func removeEngineService()     {}
func startEngineService() bool { return false }

func engineServiceState() (string, string) {
	if engineRunning() {
		return "timer", "running"
	}
	return "timer", "not_running"
}

// drainEngineForInstaller：安裝、回復、移除前，外殼（Fcitx5、0c-ibus）都沒在執行而引擎還在時，請它交接退出
// （沒有外殼就沒有組字，交接會立刻存檔結束）。0.2.0 起排程每次喚起都會把停掉的引擎叫起來，使用者照安裝器
// 的指示 pkill 之後再執行，中間只要排程跑過一次，安裝器就又說「正在使用中」。外殼還在執行時
// 不動，照舊由安裝器請使用者先結束輸入法。
func drainEngineForInstaller() {
	if !engineRunning() {
		return
	}
	if pids, err := inputProcesses("fcitx5", "0c-ibus"); err != nil || len(pids) > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := autoupdate.Drain(ctx, dialEngine, 15*time.Second); err == nil {
		waitServersStopped(ctx)
	}
}

// engineAfterInstall：安裝完成後叫起常駐引擎（macOS 另有處理，見 engine_service_darwin.go）。
func engineAfterInstall(base string) { ensureEngineService(base) }
