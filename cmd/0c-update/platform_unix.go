//go:build !windows && !darwin

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func basePath() string    { home, _ := os.UserHomeDir(); return filepath.Join(home, ".local/share/zuin") }
func receiptPath() string { return filepath.Join(basePath(), "install-receipt.json") }
func python(script string, args ...string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("目前僅提供 Linux 與 Windows 升級")
	}
	command := exec.Command("python3", append([]string{script}, args...)...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}
func install(bundle string, release *update.Release) error {
	if _, legacy := release.Files[update.LinuxLegacyServer]; legacy {
		return fmt.Errorf("此套件使用舊的 zuin-* 檔名；要回到舊版請用 rollback 指定升級時的備份目錄")
	}
	drainEngineForInstaller()
	if inputSetupFollows {
		os.Setenv("ZC_INPUT_SETUP", "1")
		defer os.Unsetenv("ZC_INPUT_SETUP")
	}
	return python(filepath.Join(bundle, "install.py"), "install")
}
func rollback(backup string, restoreUser bool) error {
	if backup == "" {
		return fmt.Errorf("Linux rollback 需要 -backup 指定安裝時顯示的備份目錄")
	}
	args := []string{"rollback", "--backup", backup}
	if restoreUser {
		args = append(args, "--restore-user")
	}
	script := filepath.Join(basePath(), "tools/install.py")
	drainEngineForInstaller()
	if err := withPointerSetAside(basePath(), backup, func() error { return python(script, args...) }); err != nil {
		return err
	}
	afterRollback(basePath(), removeSchedule)
	return nil
}

// afterRollback 清掉回復後用不到的核心版本目錄；回復的是首次安裝（回到沒有 0c 安裝）時，
// 另外照 uninstall 移除自動更新排程、排程用的穩定副本與更新狀態，不讓排程在沒有安裝時繼續執行。
func afterRollback(base string, unschedule func() error) {
	autoupdate.PruneCore(base)
	removeEngineService() // 回復的目標版本若支援常駐引擎，下一次喚起再叫起（#120）
	if installedAt(base) {
		return
	}
	if err := unschedule(); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 移除自動更新排程失敗：", err)
	}
	os.RemoveAll(filepath.Join(base, "bin"))
	os.RemoveAll(filepath.Join(base, "update"))
	fmt.Println("已回到安裝前（沒有 0c 安裝）：一併移除自動更新排程與更新工具副本。")
}

// installedAt 回報 base 下是否有有效（未移除）的完整安裝收據。
func installedAt(base string) bool {
	b, err := os.ReadFile(filepath.Join(base, "install-receipt.json"))
	if err != nil {
		return false
	}
	var r struct {
		Active *bool `json:"active"`
	}
	return json.Unmarshal(b, &r) == nil && (r.Active == nil || *r.Active)
}

// withPointerSetAside 在回復完整安裝前，把之後核心更新寫入的引擎指標暫時移開。
//
// 該次完整安裝把 engine.json 記為「已移除」，0.1.0-dev.20 以前的 install.py 見到它重新出現就拒絕回復；
// 回復用的是完整安裝時留下的 tools/install.py，核心更新不會換掉它。回復成功時由 install.py 放回安裝前的指標
// （沒有就維持刪除），失敗時放回原本的指標。
func withPointerSetAside(base, backup string, run func() error) error {
	pointer := filepath.Join(base, "engine.json")
	var transaction map[string]struct {
		After *string `json:"after"`
	}
	raw, err := os.ReadFile(filepath.Join(backup, "transaction.json"))
	if err != nil || json.Unmarshal(raw, &transaction) != nil {
		return run() // 交給 install.py 報告備份問題
	}
	entry, ok := transaction[".local/share/zuin/engine.json"]
	st, err := os.Lstat(pointer)
	if !ok || entry.After != nil || err != nil || !st.Mode().IsRegular() {
		return run()
	}
	aside := pointer + ".rollback"
	os.Remove(aside)
	if err = os.Rename(pointer, aside); err != nil {
		return err
	}
	if err = run(); err != nil {
		if _, e := os.Lstat(pointer); os.IsNotExist(e) {
			os.Rename(aside, pointer)
		}
		return err
	}
	return os.Remove(aside)
}
func uninstall() error {
	drainEngineForInstaller()
	removeEngineService() // #120：不留下登入時啟動引擎的定義
	return python(filepath.Join(basePath(), "tools/install.py"), "uninstall")
}

const restartHint = "完成組字後執行 pkill -x 0c-server，下一個按鍵會以新設定啟動引擎。"

// installIntegrity：Linux 的安裝器自己核對檔案，status 不另外顯示。
func installIntegrity() string { return "" }
