//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 與 EngineClient.swift 相同的 socket；升級後重新登入前，執行中的仍是 Zuin.app 的引擎。
func dialEngine(timeout time.Duration) (net.Conn, error) {
	home, _ := os.UserHomeDir()
	conn, err := net.DialTimeout("unix", filepath.Join(home, "Library/Caches/0c/engine.sock"), timeout)
	if err == nil {
		return conn, nil
	}
	if legacy, e := net.DialTimeout("unix", filepath.Join(home, "Library/Caches/Zuin/engine.sock"), timeout); e == nil {
		return legacy, nil
	}
	return nil, err
}

// macOS 的引擎與字典在 App 內，外殼明確指定 -data，因此一律整個 App 更新（安裝後結束舊的輸入法程式，#170）。
func coreLayout() (autoupdate.CoreLayout, bool) { return autoupdate.CoreLayout{}, false }

// macOS 的 0c-update 隨 App 整個更新，沒有另一份 PATH 上的副本。
func refreshPathUpdater(string)       { ensureSettingsEntry(); forgetBackupApps() }
func shellUnattended() bool           { return true }
func elevatedOK() bool                { return true }
func relaunchElevated([]string) error { return fmt.Errorf("不需要提權") }
func startEngine(string) error        { return nil }

var (
	hidIdle = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)
	battPct = regexp.MustCompile(`(\d+)%;\s*(\w+)`)
)

func conditions() autoupdate.Conditions {
	var c autoupdate.Conditions
	c.Idle, c.IdleKnown = hidIdleTime()
	if out, err := exec.Command("pmset", "-g", "batt").Output(); err == nil {
		c.Charging = bytes.Contains(out, []byte("AC Power"))
		if m := battPct.FindSubmatch(out); m != nil {
			c.Battery, _ = strconv.Atoi(string(m[1]))
			c.BatteryKnown = true
			if s := string(m[2]); s == "charging" || s == "charged" {
				c.Charging = true
			}
		}
	}
	return c
}

func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func notify(n autoupdate.Notice) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(installedApp(), "Contents/MacOS/0c"), "--update-notice")
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}

const agentLabel = "com.biggo.0c.update"

// agentPath 是 0.1.0-dev.38 以前的排程位置：使用者 LaunchAgents 下的 plist，執行 Application Support 內的穩定副本。
// 系統設定的背景項目只能顯示簽章的公司名稱（#25）。新版 App 內附 plist，改以 SMAppService 註冊。
func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/LaunchAgents", agentLabel+".plist")
}

// appAgent 是 App 內附的排程 plist；有它的 App 以 SMAppService 註冊排程。
func appAgent(app string) string { return bundledAgentPlist(app) }

func installedApp() string { return filepath.Join(paths().InputMethods, update.MacApp) }

// agentState 以 App 本身的執行檔查詢或變更 SMAppService 排程（SMAppService 依呼叫者的 main bundle 找 plist）。
// 回傳 enabled、requires-approval（使用者在系統設定關閉了背景執行）、not-registered、not-found。
func agentState(app, action string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, filepath.Join(app, "Contents/MacOS/0c"), "--update-agent", action).Output()
	state := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if state == "" {
		if err == nil {
			err = fmt.Errorf("沒有回報狀態")
		}
		return "", fmt.Errorf("排程狀態查詢失敗：%w", err)
	}
	return state, nil
}

// unregisterMovedAgent 以移入備份的 App 取消註冊。App 剛搬走時系統還沒登記新位置，取消不會生效
// （macOS 26 實測，稍後再取消即可），所以重試到狀態不再是啟用，最多約 10 秒。
func unregisterMovedAgent(app string) {
	if !fileExists(appAgent(app)) {
		return
	}
	for i := 0; i < 10; i++ {
		if state, err := agentState(app, "unregister"); err == nil && state != "enabled" {
			return
		}
		time.Sleep(time.Second)
	}
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func removeLegacyAgent() {
	path := agentPath()
	if _, err := os.Lstat(path); err != nil {
		return
	}
	// Legacy and SMAppService agents share a label. A stale plist does not
	// mean the loaded service is legacy; bootout of an App service also kills
	// the updater executing this function before it can save shell pending.
	domain := fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)
	if out, err := exec.Command("launchctl", "print", domain).Output(); err == nil {
		loadedPath := regexp.MustCompile(`(?m)^\s*path = (.+)$`).FindSubmatch(out)
		if len(loadedPath) == 2 && string(loadedPath[1]) == path && !bytes.Contains(out, []byte("managed_by = com.apple.xpc.ServiceManagement")) {
			exec.Command("launchctl", "bootout", domain).Run()
		}
	}
	os.Remove(path)
}

// installSchedule 建立自動更新排程：登入時與每 15 分鐘喚起；查詢頻率由協調器決定。
// 新版 App 以 SMAppService 註冊 App 內附的 agent，並移除舊的 LaunchAgents plist（同一個 Label 不能並存）。
func installSchedule(base string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if bundleUpdater != "" {
		// 背景更新由舊的穩定副本執行：改用新套件內附的版本，下次更新才會用新的安裝邏輯。
		self = bundleUpdater
	}
	stable := stableUpdater(base)
	if err = replaceUpdater(self, stable); err != nil {
		return err
	}
	if app := installedApp(); fileExists(appAgent(app)) {
		removeLegacyAgent()
		state, err := agentState(app, "register")
		if err != nil {
			return err
		}
		fmt.Println(agentStatusText(state))
		return nil
	}
	return installLegacyAgent(stable, base)
}

func installLegacyAgent(stable, base string) error {
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>auto</string></array>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>LowPriorityIO</key><true/>
  <key>StandardOutPath</key><string>/dev/null</string>
  <key>StandardErrorPath</key><string>/dev/null</string>
</dict>
</plist>
`, agentLabel, xmlEscape(stable), int(scheduleEvery(base).Seconds()))
	path := agentPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	exec.Command("launchctl", "bootout", domain+"/"+agentLabel).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		// 沒有圖形登入（例如只有 SSH）時沒有 gui 網域；下次登入時 launchd 會自動載入。
		fmt.Printf("排程已寫入，將在下次登入時載入（%s）\n", bytes.TrimSpace(out))
		return nil
	}
	fmt.Println("已建立自動更新排程（launchd " + agentLabel + "）")
	return nil
}

// agentStatusText 是給使用者看的排程狀態；使用者關閉背景執行時說明影響與重新開啟的位置。
func agentStatusText(state string) string {
	switch state {
	case "enabled":
		return "排程：已啟用（系統設定的背景項目顯示為「靈犀注音」）"
	case "requires-approval":
		return "排程：已在系統設定關閉，不會自動更新。要恢復請到「系統設定 → 一般 → 登入項目與延伸功能」，在「允許在背景執行」開啟「靈犀注音」（或執行 0c-update schedule open-settings）"
	case "not-registered":
		return "排程：未建立"
	default:
		return "排程：狀態不明（" + state + "）"
	}
}

func removeSchedule() error {
	if app := installedApp(); fileExists(appAgent(app)) {
		if _, err := agentState(app, "unregister"); err != nil {
			return err
		}
	}
	removeLegacyAgent()
	fmt.Println("已移除自動更新排程")
	return nil
}

func scheduleStatus() string {
	if app := installedApp(); fileExists(appAgent(app)) {
		state, err := agentState(app, "status")
		if err != nil {
			return "排程：" + err.Error()
		}
		return agentStatusText(state)
	}
	if _, err := os.Stat(agentPath()); err != nil {
		return "排程：未建立"
	}
	if exec.Command("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)).Run() == nil {
		return "排程：已載入"
	}
	return "排程：已建立（下次登入載入）"
}

// openScheduleSettings 開啟系統設定的登入項目頁，讓使用者重新允許背景執行。
func openScheduleSettings() error {
	app := installedApp()
	if !fileExists(appAgent(app)) {
		return fmt.Errorf("目前安裝的 App 沒有內附排程")
	}
	_, err := agentState(app, "open-settings")
	return err
}

// deferShell：外殼更新不需延到登入。
func deferShell() bool { return false }

// The App's UNUserNotificationCenter delegate handles the later action. The
// scheduler never stays alive waiting for a click and never opens a window.
func notifyWithAction(_ context.Context, n autoupdate.Notice) (string, error) {
	return "", notify(n)
}

// setupInputMethod：此平台由安裝程式註冊輸入法，不需額外設定。
func setupInputMethod(string) error { return nil }

// deferInputSource 記下要啟用的輸入來源：新裝的 App 要重新登入後系統才登記得到，由之後的排程喚起補做（addInputMethod）。
// 只寫記號，不動狀態檔：安裝多半在協調器手上拿著狀態時進行，這裡存的欄位會被協調器存檔蓋掉（#195）。
// 安裝、回復當下已在這個登入工作階段試過一次，記下來，排程跨過登入邊界才再試（見 inputSourceRetry）。
func deferInputSource(tool, app string) error {
	base := basePath()
	if err := os.MkdirAll(filepath.Dir(inputSourceMarker(base)), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(inputSourceMarker(base), []byte(tool+"\n"+app+"\n"), 0o600); err != nil {
		return err
	}
	cur := macLoginStamp(time.Now())
	saveInputSourceRetry(base, inputSourceRetry{ASID: cur.ASID, At: cur.At, Headless: cur.Headless})
	return nil
}

// inputSourcePending：安裝或回復時有輸入來源還沒啟用成功（不靠狀態檔的 ProfileAtLogin，#195）；要不要在這次喚起補做由
// addInputMethod 依 inputSourceRetry 決定。
func inputSourcePending(base string) bool { return fileExists(inputSourceMarker(base)) }

// addInputMethod 補做安裝或回復時沒啟用成功的輸入來源（見 retryInputSource）；不補做時不查詢。
func addInputMethod() error { return retryInputMethod(false) }

// retryInputMethod：scheduled 是背景排程的 auto，只有它在不補做時查詢使用者是不是已經自己加入（見 retryInputSource）。
func retryInputMethod(scheduled bool) error {
	return retryInputSource(basePath(), macLoginStamp(time.Now()), macBootTime(), scheduled, activateInputSource, inputSourceEnabled)
}

// inputSourceEnabled 查詢 app 的輸入來源是否已啟用：以 tool 在新的行程回讀 TIS（本行程的 TIS 有快取），只查詢、
// 不登記、不啟用也不改選，不會跳系統詢問、不會切換使用者正在用的輸入來源。tool 的版本太舊、讀不到或逾時都當成還沒啟用。
func inputSourceEnabled(tool, app string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !queryInputSourceSupported(bundleBuildVersion(ctx, filepath.Dir(filepath.Dir(filepath.Dir(tool))))) {
		return false
	}
	out, err := exec.CommandContext(ctx, tool, "--query-input-source", app).Output()
	return err == nil && confirmedInputSource(string(out))
}

// scheduleExists：修復只針對 Windows 防毒軟體移除排程的情況；這裡不檢查。
func scheduleExists() bool { return true }
