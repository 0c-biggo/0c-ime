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

	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/update"
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
func appAgent(app string) string {
	return filepath.Join(app, "Contents/Library/LaunchAgents", agentLabel+".plist")
}

func installedApp() string { return filepath.Join(paths().InputMethods, update.MacApp) }

// agentState 以 App 本身的執行檔查詢或變更 SMAppService 排程（SMAppService 依呼叫者的 main bundle 找 plist）。
// 回傳 enabled、requires-approval（使用者在系統設定關閉了背景執行）、not-registered、not-found。
func agentState(app, action string) (string, error) {
	out, err := exec.Command(filepath.Join(app, "Contents/MacOS/0c"), "--update-agent", action).Output()
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

func inputSourceMarker(base string) string { return filepath.Join(base, "update", "input-source") }

// deferInputSource 記下要啟用的輸入來源：新裝的 App 要重新登入後系統才登記得到，由登入時的 auto 補做。
func deferInputSource(tool, app string) error {
	base := basePath()
	if err := os.MkdirAll(filepath.Dir(inputSourceMarker(base)), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(inputSourceMarker(base), []byte(tool+"\n"+app+"\n"), 0o600); err != nil {
		return err
	}
	s, err := autoupdate.Load(base)
	if err != nil {
		return err
	}
	s.ProfileAtLogin = true
	return autoupdate.Save(base, s)
}

// addInputMethod 啟用安裝或回復時記下的輸入來源；成功後清除記錄，失敗則留待下次喚起。
func addInputMethod() error {
	b, err := os.ReadFile(inputSourceMarker(basePath()))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		os.Remove(inputSourceMarker(basePath()))
		return nil
	}
	if err = activateInputSource(lines[0], lines[1]); err != nil {
		return err
	}
	return os.Remove(inputSourceMarker(basePath()))
}

// scheduleExists：修復只針對 Windows 防毒軟體移除排程的情況；這裡不檢查。
func scheduleExists() bool { return true }
