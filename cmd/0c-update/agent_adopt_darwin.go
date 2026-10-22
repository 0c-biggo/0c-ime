//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// adoptSchedule 讓背景項目的登記留在已安裝的 App（#205，原因見 agent_backup_mac.go）：把備份裡 App 內附的排程 plist
// 改名，舊輸入法程式就拉不走登記；有改名時以已安裝的 App 註冊一次，把已經被拉走的登記拉回來，下一次喚起排程就執行
// 新版的 0c-update，不必重新登入。launchd 啟動不了排程（登記指著的備份已刪除）時取消再重新註冊。
//
// status、check、auto、finish 都先做這一步：仍在執行的舊輸入法程式的選單每 15 秒以啟動時的路徑（就是 Input Methods
// 裡新版的 0c-update）查詢 status，設定程式、選單「檢查更新」、排程也都經過這裡，所以舊版更新器在背景裝好新版後，
// 新版在幾秒內就接手。平常只多讀一次 backups 目錄；launchctl print 在 check、auto、finish 每次都做，status 只在剛改名、
// 改回 plist 或有 kick 記號時做，其他時候每 15 分鐘一次（needsLaunchdCheck：看登記指著的備份被刪掉的 spawn failed）。
var adoptSchedule = adoptInstalledSchedule // 測試換掉：go test 不動這台機器的登記與備份

// foreground 為 false 時（status：選單每 15 秒查一次，5 秒就被結束）需要重新註冊的話交給另一個行程（schedule adopt），
// 免得取消之後、重新註冊之前被結束，排程就不見了。
func adoptInstalledSchedule(foreground bool) {
	app := installedApp()
	o := agentObservation{Restored: restoreBundledAgent(app)}
	if !fileExists(appAgent(app)) {
		return // 沒有內附排程的 App（或沒有安裝）：舊的 LaunchAgents plist 不受影響
	}
	base := basePath()
	o.Quieted = quietBackupAgents(filepath.Join(base, "backups"))
	marker := filepath.Join(base, "update", "agent-kick")
	pending := readKickMarker(marker)
	if !launchdCheckDue(base, foreground, o, pending, time.Now()) {
		return // 選單的 status：沒有要拉回的登記、沒有 kick 記號，最近也看過 launchd（每個舊選單每 15 秒少一個 launchctl）
	}
	running, runs := false, 0
	out, err := launchctlPrintAgent()
	noteLaunchdCheck(base)
	if err == nil {
		o.Broken, running, runs = agentSpawnFailed(out), agentRunning(out), agentRuns(out)
	}
	o.InJob = os.Getenv("XPC_SERVICE_NAME") == agentLabel
	pulled := false
	if o.needsState() {
		if s, err := autoupdate.Load(basePath()); err == nil {
			o.Removed = s.ScheduleRemoved
		}
		if state, err := agentState(app, "status"); err == nil {
			o.State = state
			switch o.adoption() {
			case adoptPull:
				state, err = agentState(app, "register")
				pulled = err == nil && state == "enabled"
			case adoptReregister:
				if foreground {
					reregisterAgent(app) // 重新註冊時 RunAtLoad 已經執行一次
				} else {
					spawnAdopt()
				}
			}
		}
	}
	k := kickAfterAdoption(pulled, pending, runs, running, o.InJob)
	switch {
	case k.Marker < 0 && fileExists(marker):
		os.Remove(marker)
	case k.Marker >= 0 && k.Marker != pending:
		_ = os.WriteFile(marker, []byte(fmt.Sprintf("%d\n", k.Marker)), 0o600)
	}
	if k.Kick {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "/bin/launchctl", "kickstart", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)).Run()
	}
}

// readKickMarker 讀 agentKick 的記號；沒有、讀不懂或超過一小時（runs 在重新登入後從頭算）時為 -1。
func readKickMarker(path string) int {
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > time.Hour {
		return -1
	}
	b, err := os.ReadFile(path)
	n := -1
	if err == nil {
		if _, e := fmt.Sscan(string(b), &n); e != nil || n < 0 {
			n = -1
		}
	}
	return n
}

// spawnAdopt 以脫離的行程（setsid）執行 0c-update schedule adopt：呼叫者結束時不會一起被結束。
func spawnAdopt() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "schedule", "adopt")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// 不接呼叫者的輸出：status -json 的讀取端要等所有寫入端關閉才讀完。
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		defer devnull.Close()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

var launchctlPrintAgent = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)).CombinedOutput()
	return string(out), err
}

// reregisterAgent 取消再重新註冊（只在 launchd 啟動不了排程時，那時排程沒有在執行，取消不會結束任何程式）。
// 同時只有一個行程做，十分鐘內不重做；取消後系統要一點時間才接受重新註冊（論壇回報約 0.5 秒），重試幾次。
// 註冊沒成功時下次再試；使用者的系統設定不受影響（只有啟用中的登記才會走到這裡）。
func reregisterAgent(app string) {
	_ = autoupdate.WithLock(basePath(), "agent-adopt.lock", func() error {
		stamp := filepath.Join(basePath(), "update", "agent-reregister")
		if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 10*time.Minute && time.Since(info.ModTime()) >= 0 {
			return nil
		}
		_ = os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
		if _, err := agentState(app, "unregister"); err != nil {
			return err
		}
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			if state, err := agentState(app, "register"); err == nil && state == "enabled" {
				return nil
			}
		}
		return fmt.Errorf("重新註冊排程沒有成功")
	})
}
