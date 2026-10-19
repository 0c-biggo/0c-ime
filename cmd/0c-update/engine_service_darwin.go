package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// 引擎服務沿用 com.biggo.0c.update 的 RunAtLoad 與每 15 分鐘排程。
// 每次喚起檢查引擎，沒在跑就啟動；以 setsid 脫離 agent 的行程群組，
// 並經 taskpolicy -a -d default 啟動，避免沿用 Background agent 的資源優先權。
// 常駐引擎不帶 -idle-exit，讓設定程式與本機 API 在沒有輸入時也能連線。
// 使用者關閉背景執行時，輸入法 App 仍可在第一個按鍵啟動引擎。

const taskpolicy = "/usr/sbin/taskpolicy"

func ensureEngineService(string) {
	if c, err := dialEngine(time.Second); err == nil {
		c.Close()
		return
	}
	if err := spawnResident(); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 引擎沒有啟動（輸入法會在下一個按鍵自己啟動）：", err)
	}
}

// engineAfterInstall 在安裝完成後叫起常駐引擎。雙擊 .pkg 時安裝由 postinstall 執行，腳本結束後安裝程式
// 刪掉腳本目錄後，syspolicyd 可能終止由 postinstall 衍生、仍在評估中的引擎。
// 為避免等到下一次排程才有常駐引擎，不是 agent 自己在安裝時就改請
// 更新 agent 立刻跑一次（launchctl kickstart），由 agent 啟動；agent 沒註冊或被關閉時才自己啟動。
func engineAfterInstall(base string) {
	if c, err := dialEngine(time.Second); err == nil {
		c.Close()
		return
	}
	if os.Getenv("XPC_SERVICE_NAME") != agentLabel && kickstartAgent() == nil {
		return
	}
	ensureEngineService(base)
}

// kickstartAgent 測試替換用。agent 正在執行時 kickstart 不會再跑一次，但那時呼叫者就是 agent（上面已排除）。
var kickstartAgent = func() error {
	return exec.Command("/bin/launchctl", "kickstart", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)).Run()
}

// spawnResident 測試替換用：go test 不啟動這台機器上安裝的引擎。
var spawnResident = spawnServiceEngine

func spawnServiceEngine() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	app := installedApp()
	server := filepath.Join(app, "Contents/MacOS/0c-server")
	if _, err := os.Stat(server); err != nil {
		return err
	}
	support, runtime := paths().Support, filepath.Join(home, "Library/Caches/0c")
	for _, dir := range []string{support, runtime} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	args := []string{"-a", "-d", "default", server,
		"-data", filepath.Join(app, "Contents/Resources/data"),
		"-user", filepath.Join(support, "user.json"),
		"-socket", filepath.Join(runtime, "engine.sock")}
	cmd := exec.Command(taskpolicy, args...)
	if _, err := os.Stat(taskpolicy); err != nil { // 沒有 taskpolicy（不太可能）：直接啟動，優先權較低但能用
		cmd = exec.Command(server, args[3:]...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func removeEngineService()     {}
func startEngineService() bool { return false } // macOS 一律整個 App 更新；交接後由 App 或下一次 agent 喚起啟動

func engineServiceState() (string, string) {
	if c, err := dialEngine(time.Second); err == nil {
		c.Close()
		return "update_agent", "running"
	}
	return "update_agent", "not_running"
}
