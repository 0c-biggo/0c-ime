package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

// 引擎服務（#120，Windows）：登入工作 0c-engine-<SID>（dev.56 起）本來就在登入時啟動引擎，不改工作定義
// （它由提權的安裝程式建立，一般權限改不了，見 docs/engine-service.md 3.3）。0.2.0 起自動更新每次
// 喚起也檢查引擎：沒在跑（例如當掉）就啟動，搜尋框這類沙箱應用程式不必等一般應用程式的下一個按鍵。
func ensureEngineService(base string) {
	if c, err := dialEngine(time.Second); err == nil {
		c.Close()
		return
	}
	if version, err := installedVersion(); err != nil || version == "" {
		return
	}
	if err := startResident(base); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 引擎沒有啟動：", err)
	}
}

// startResident 測試替換用：go test 不執行這台機器上的登入工作、不啟動安裝的引擎。
var startResident = func(base string) error {
	return startResidentEngine(base, elevatedOK(), taskExists(engineTaskName()), runEngineTask, spawnEngine)
}

func runEngineTask() error {
	return quietCommand(schtasksPath(), "/Run", "/TN", engineTaskName()).Run()
}

// startResidentEngine 一律先請登入工作啟動引擎，不由呼叫者直接啟動：
//   - 自動更新工作以 Priority 7 執行，直接啟動的引擎會帶著低 I/O 與記憶體優先順序常駐到登出（startEngine 的註解）；
//   - 安裝程式以系統管理員執行 afterInstall，直接啟動的引擎會以系統管理員權限常駐，個人詞庫也由它寫入。
//
// 登入工作不在或執行失敗時，一般權限照舊直接啟動；系統管理員則不啟動，留給 DLL 在下一個按鍵以使用者權限啟動。
func startResidentEngine(base string, elevated, hasTask bool, runTask func() error, spawn func(string) error) error {
	if hasTask {
		err := runTask()
		if err == nil {
			return nil
		}
		if elevated {
			return fmt.Errorf("登入工作沒有執行（%v）；不以系統管理員權限啟動，下一個按鍵會啟動", err)
		}
	} else if elevated {
		return fmt.Errorf("沒有登入工作；不以系統管理員權限啟動，下一個按鍵會啟動")
	}
	return spawn(base)
}

// handOverAfterInstall（install、rollback）：引擎指標換好之後，常駐的引擎還是換之前的版本時，請它在沒有組字時存檔交接。
// 0.2.0 起引擎由登入工作常駐、不閒置結束，不交接的話舊引擎會一直跑到登出：介面依新版的 0c-update 顯示「我的詞彙」，
// 引擎卻還是不支援的舊版。
// 交接後由登入工作叫起指標所指的版本：安裝在 afterInstall（穩定副本換新之後）、回復在這裡（start）。
// 仍在組字就不換，照舊登出再登入後生效。執行中應用程式裡的舊 DLL 會連上新引擎，與核心更新相同。
func handOverAfterInstall(version string, start func()) {
	handOverResidentEngine(version, func() (map[string]string, bool) {
		info, running, _ := autoupdate.Health(dialEngine)
		return info, running
	}, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return autoupdate.Drain(ctx, dialEngine, 40*time.Second)
	}, start)
}

func handOverResidentEngine(version string, health func() (map[string]string, bool), drain func() error, start func()) {
	info, running := health()
	if !running || version == "" || info["version"] == version {
		return
	}
	if err := drain(); err != nil {
		fmt.Println("引擎仍在使用中，沒有換成新版；完成組字後登出再登入即生效。")
		return
	}
	if start != nil {
		start()
	}
}

func removeEngineService()                 {}
func startEngineService() bool             { return false } // 交接照舊：有登入工作時 schtasks /Run
func engineServiceState() (string, string) { return "login_task", "" }

// engineAfterInstall：安裝完成後叫起常駐引擎（macOS 另有處理，見 engine_service_darwin.go）。
func engineAfterInstall(base string) { ensureEngineService(base) }
