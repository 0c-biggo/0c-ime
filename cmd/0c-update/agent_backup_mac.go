//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// macOS 背景項目跟到備份裡的 App（#205）。只動檔案與解析文字，放在這裡讓 Linux 上也跑得到單元測試；
// 查詢與變更系統登記的部分在 agent_adopt_darwin.go。
//
// SMAppService 的排程（com.biggo.0c.update）在背景項目資料庫裡記著登記它的 App 的位置，排程執行的是那個位置的
// Contents/MacOS/0c-update。只要有 App 呼叫 SMAppService（查詢狀態、註冊），登記就改成呼叫者所在的位置
// （macOS 14、15 VM 實測）。更新時舊 App 整包移入 backups/，仍在執行的舊輸入法程式選單每次重建（最多每 2 秒）都查詢
// 一次排程狀態；macOS 15 以它現在所在的位置（備份）當呼叫者，登記就被拉到備份，排程一直執行備份裡的舊 0c-update，
// 新版更新器的修正到重新登入前都不生效（macOS 14 以啟動時的路徑當呼叫者，所以沒有這個問題）。
//
// 備份裡的 App 沒有內附的排程 plist 時，查詢只回報找不到、不會拉走登記（VM 實測），所以把備份裡的 plist 改名；
// 已發布的舊版程式也一樣適用，不必等它們結束。回復時先改回原名（restoreBundledAgent），放回的 App 與收據、簽章一致。

const inactiveAgentSuffix = ".inactive"

// bundledAgentPlist 是 App 內附的排程 plist（SMAppService.agent(plistName:) 找的位置）。
func bundledAgentPlist(app string) string {
	return filepath.Join(app, "Contents/Library/LaunchAgents", macAgentLabel+".plist")
}

// quietBackupAgents 把 backups 下每份安裝備份（macos-<時間>-<亂數>/0c.app）內附的排程 plist 改名，回傳這次改了幾份。
// 只改一般檔案；讀不到、改不動時跳過，下次再試。
func quietBackupAgents(backups string) int {
	// 安裝、回復進行中（持有 update.lock）時不動：回復要放回的那份剛由 PrepareRollback 改回原名。一小時以前留下的鎖不算。
	if info, err := os.Stat(filepath.Join(filepath.Dir(backups), "update.lock")); err == nil && time.Since(info.ModTime()) < time.Hour {
		return 0
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || !macBackupDir.MatchString(e.Name()) {
			continue
		}
		p := bundledAgentPlist(filepath.Join(backups, e.Name(), "0c.app"))
		if info, err := os.Lstat(p); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, err := os.Lstat(p + inactiveAgentSuffix); err == nil {
			continue // 不覆蓋
		}
		if os.Rename(p, p+inactiveAgentSuffix) == nil {
			n++
		}
	}
	return n
}

// restoreBundledAgent 把 quietBackupAgents 改名的 plist 改回原名（app 要放回 Input Methods 或已經放回時）；有改回時回傳 true。
// 原名的 plist 已存在時不動。
func restoreBundledAgent(app string) bool {
	p := bundledAgentPlist(app)
	if _, err := os.Lstat(p); err == nil {
		return false
	}
	info, err := os.Lstat(p + inactiveAgentSuffix)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return os.Rename(p+inactiveAgentSuffix, p) == nil
}

// agentAdoption 是 adoptSchedule 這次要對登記做的事。
type agentAdoption int

const (
	adoptNothing    agentAdoption = iota
	adoptPull                     // 以已安裝的 App 註冊一次（已註冊時不變更設定），把登記拉回 Input Methods
	adoptReregister               // 取消再重新註冊：launchd 啟動不了排程時
)

// agentObservation 是 adoptSchedule 觀察到的情形。
type agentObservation struct {
	Quieted  int    // 這次改名的備份 plist 數
	Restored bool   // 已安裝的 App 的 plist 剛改回原名（舊版回復放回了改名過的 App）
	Broken   bool   // launchd 啟動不了排程（agentSpawnFailed）
	State    string // 已安裝的 App 查到的 SMAppService 狀態；沒查時為空字串
	InJob    bool   // 這個行程就是排程本身（launchd 的 XPC_SERVICE_NAME）：取消註冊會連自己一起結束
	Removed  bool   // 使用者以 schedule remove 關閉了排程
}

// needsState：要不要查詢已安裝的 App 的排程狀態（啟動一次 App 的執行檔）。登記沒有被拉走的可能、launchd 也正常時不查，
// 輸入法選單每 15 秒的 status 不必多花時間。
func (o agentObservation) needsState() bool { return o.Quieted > 0 || o.Restored || o.Broken }

// adoption 決定要做的事。只對已啟用的登記動手：使用者在系統設定關閉（requires-approval）時不碰，免得重新啟用或再跳通知；
// 沒有註冊的只在剛改回 plist、且不是使用者自己移除時補註冊。取消註冊會結束正在執行的排程，排程本身不做。
func (o agentObservation) adoption() agentAdoption {
	switch {
	case o.State == "enabled" && o.Broken && !o.InJob:
		return adoptReregister
	case o.State == "enabled" && (o.Quieted > 0 || o.Restored):
		return adoptPull
	case o.State == "not-registered" && o.Restored && !o.Removed:
		return adoptPull
	}
	return adoptNothing
}

// agentKick 是把登記拉回來之後，等新版的 0c-update 跑起來的記號（update/agent-kick，內容是記下時 launchd 的 runs）。
// 拉回來之後請 launchd 立刻執行一次排程，新版馬上接手（重新判斷狀態、清掉舊版留下的「重新登入後生效」），不必等下一次
// 15 分鐘的喚起。排程正在執行（多半是剛裝好新版、還沒結束的舊版更新器）時 kickstart 不會再跑一次；剛結束的排程 launchd
// 也可能先不接受（VM 實測一次沒有跑），所以留著記號，之後每次經過這裡（選單每 15 秒的 status）再請，直到 runs 比記下的多
// （排程結束之後又跑過一次）。排程本身經過這裡時就是新版在跑，清掉記號。
type agentKick struct {
	Kick   bool
	Marker int // 要留的記號（記下的 runs）；-1 表示刪掉
}

// kickAfterAdoption：pulled 是這次把登記拉回來了；marker 是記號裡的 runs（沒有記號為 -1）；runs、running 取自 launchctl print。
func kickAfterAdoption(pulled bool, marker, runs int, running, inJob bool) agentKick {
	switch {
	case inJob:
		return agentKick{Marker: -1}
	case pulled:
		marker = runs // 從現在起要再跑一次
	case marker < 0:
		return agentKick{Marker: -1}
	case runs > marker && !running:
		return agentKick{Marker: -1} // 記下之後已經跑完一次
	}
	return agentKick{Kick: !running, Marker: marker}
}

// bundlesAgent：App 內附排程 plist（包括被 quietBackupAgents 改名的）。
func bundlesAgent(app string) bool {
	for _, p := range []string{bundledAgentPlist(app), bundledAgentPlist(app) + inactiveAgentSuffix} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

var (
	agentJobSpawnFailed = regexp.MustCompile(`(?m)^\s*job state = spawn failed\s*$`)
	agentExitConfig     = regexp.MustCompile(`(?m)^\s*last exit code = 78\b`)
	agentJobRunning     = regexp.MustCompile(`(?m)^\s*state = running\s*$`)
	agentJobRuns        = regexp.MustCompile(`(?m)^\s*runs = (\d+)\s*$`)
)

// agentRunning：`launchctl print` 顯示排程正在執行。
func agentRunning(out string) bool { return agentJobRunning.MatchString(out) }

// agentRuns：`launchctl print` 的 runs（這個登入工作階段 launchd 啟動過排程幾次）；沒有時為 0。
func agentRuns(out string) int {
	n := 0
	if m := agentJobRuns.FindStringSubmatch(out); m != nil {
		fmt.Sscan(m[1], &n)
	}
	return n
}

// agentSpawnFailed：launchd 啟動不了排程（`launchctl print` 的 job state 為 spawn failed，或結束碼 78 EX_CONFIG），
// 例如登記指著的備份已被刪除（VM 實測）。之後即使登記改回已安裝的 App 也一直失敗，要取消再重新註冊才恢復。
func agentSpawnFailed(out string) bool {
	return agentJobSpawnFailed.MatchString(out) || agentExitConfig.MatchString(out)
}
