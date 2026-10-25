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

// adoptSchedule 保留每份 App 的完整簽章（#213）。舊版外殼可能把 SMAppService 登記拉回備份（#205），
// 所以只在使用者離開、舊外殼安全退場後接手。舊選單每 15 秒查 installed updater 的 status，
// 可以喚起一次有界 schedule adopt；不跑下載／安裝協調器，不新增常駐服務。
var adoptSchedule = adoptInstalledSchedule // 測試換掉：go test 不動這台機器的登記與備份

// foreground 為 false 時（status：選單每 15 秒查一次，5 秒就被結束）需要重新註冊的話交給另一個行程（schedule adopt），
// 免得取消之後、重新註冊之前被結束，排程就不見了。
func adoptInstalledSchedule(foreground bool) {
	adoptScheduleAt(basePath(), installedApp(), foreground)
}

func adoptScheduleAt(base, app string, foreground bool) {
	restoreBackupAgents(filepath.Join(base, "backups"))
	o := agentObservation{Restored: restoreBundledAgent(app)}
	if !fileExists(appAgent(app)) {
		return // 沒有內附排程的 App（或沒有安裝）：舊的 LaunchAgents plist 不受影響
	}
	if s, err := autoupdate.Load(base); err != nil || s.ScheduleRemoved {
		return
	}
	ready, pulled := adoptCurrentShell(base, app, foreground)
	if !ready {
		return
	}
	o.Rebound = pulled
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
	if !pulled && o.needsState() {
		if s, err := autoupdate.Load(base); err == nil {
			o.Removed = s.ScheduleRemoved
		}
		if state, err := agentScheduleState(app, "status"); err == nil {
			o.State = state
			switch o.adoption() {
			case adoptPull:
				state, err = agentScheduleState(app, "register")
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
	// 使用者在登記之後關閉背景執行時，不再重試 kick。
	if pending >= 0 && !pulled {
		if state, err := agentScheduleState(app, "status"); err != nil || state != "enabled" {
			return
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
		kickAgent()
	}
}

// adoptCurrentShell 以 installed executable 的檔案身分記錄已接管；同版重裝／rollback 也會重新判斷。
// status 不等待退場：它的呼叫者五秒後會結束它，改用既有 detached schedule adopt 完成一次有界工作。
func adoptCurrentShell(base, app string, foreground bool) (ready, pulled bool) {
	r, err := readMacReceiptFile(filepath.Join(base, "install-receipt.json"))
	if err != nil || r == nil || !r.Active {
		return false, false
	}
	current := filepath.Join(app, "Contents/MacOS/0c")
	identity := agentInstallIdentity(base, current)
	if identity == "" || macInstallLocked(base) {
		return false, false
	}
	if agentTakeoverDone(base, identity) {
		return true, false
	}
	if agentReloginRequired(base) {
		return false, false
	}
	if !foreground {
		old, err := observeMacShells(current)
		if err == nil && (len(old) == 0 || userAway()) && claimAgentSpawn(base, identity, time.Now()) {
			spawnAdopt()
		}
		return false, false
	}
	_ = autoupdate.WithLock(base, "agent-takeover.lock", func() error {
		if macInstallLocked(base) || agentReloginRequired(base) || agentInstallIdentity(base, current) != identity {
			return nil
		}
		if agentTakeoverDone(base, identity) {
			ready = true
			return nil
		}
		s, err := autoupdate.Load(base)
		if err != nil || s.ScheduleRemoved {
			return err
		}
		// 與外殼 UpdateAgentMonitor.check 一致：沒有使用者移除記號時可補註冊；
		// requires-approval 則保留。status 先修回 inactive 後，理由不必依賴單一行程的布林值。
		state, err := agentScheduleState(app, "status")
		if err != nil || state != "enabled" && state != "not-registered" {
			return err
		}
		c := agentTakeoverControl{
			identity: func() string { return agentInstallIdentity(base, current) },
			shells:   func() ([]uiProcess, error) { return observeMacShells(current) },
			away:     userAway,
			retire:   func() bool { return retireForAdoption(current) },
			pull: func() bool {
				// 等待退場時可能關閉了背景執行／移除排程；登記前重讀，不覆蓋使用者的新選擇。
				s, err := autoupdate.Load(base)
				if err != nil || s.ScheduleRemoved || macInstallLocked(base) {
					return false
				}
				state, err := agentScheduleState(app, "status")
				if err != nil || state != "enabled" && state != "not-registered" {
					return false
				}
				state, err = agentScheduleState(app, "register")
				return err == nil && state == "enabled"
			},
		}
		if !finishAgentTakeover(identity, c) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(agentTakeoverStamp(base)), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(agentTakeoverStamp(base), []byte(identity), 0o600); err != nil {
			return err
		}
		ready, pulled = true, true
		return nil
	})
	return ready, pulled
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
// 系統操作集中為可替換的 hooks；原生 go test 不啟動自己的副本或操作背景項目。
var (
	agentScheduleState   = agentState
	agentReloginRequired = macReloginRequired
	retireForAdoption    = func(current string) bool {
		_, left := retireMacShells(current, uiWaitAuto, true)
		return len(left) == 0
	}
	spawnAdopt = spawnScheduleAdopt
	kickAgent  = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "/bin/launchctl", "kickstart", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)).Run()
	}
)

func spawnScheduleAdopt() {
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
		if _, err := agentScheduleState(app, "unregister"); err != nil {
			return err
		}
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			if state, err := agentScheduleState(app, "register"); err == nil && state == "enabled" {
				return nil
			}
		}
		return fmt.Errorf("重新註冊排程沒有成功")
	})
}
