//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// 每次更新都把舊 0c.app 整包移入 backups/，移動後 LaunchServices 仍記著它，且與新版同一個 bundle ID。
// dev.58 以前各版的 CFBundleVersion 都是 1，系統分不出哪份較新，可能開到備份裡的舊外殼：候選窗回到
// #67 的位置，更新也一直停在「重新登入後生效」（#77）。這裡把備份裡的 App 從 LaunchServices 取消登記；
// 回復時 RollbackMac 把 App 搬回 Input Methods，再由 registerInstalledApp 重新登記。
const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// lsForgotten 記下已取消登記的備份目錄，之後每次背景更新只處理新出現的備份。
const lsForgotten = ".launchservices-forgotten"

func backupBundles(dir string) []string {
	var out []string
	for _, pattern := range []string{"*.app", "*.app/Contents/Helpers/*.app", "*.app/Contents/Library/*.app"} {
		m, _ := filepath.Glob(filepath.Join(dir, pattern))
		out = append(out, m...)
	}
	return out
}

// runLSRegister 的最後一個參數是 App 路徑。backups 是符號連結時（例如移到外接磁碟），LaunchServices
// 可能記著解析後的路徑，也可能記著搬移前的連結路徑；兩個都送，連結路徑掃不到時的錯誤（-10814）忽略。
// 任一路徑成功即回傳 true；沒有 lsregister 或全部失敗時回傳 false，呼叫端下次再試。
func runLSRegister(args ...string) bool {
	if _, err := os.Stat(lsregister); err != nil || len(args) == 0 {
		return false
	}
	paths := []string{args[len(args)-1]}
	if real, err := filepath.EvalSymlinks(paths[0]); err == nil && real != paths[0] {
		paths = append([]string{real}, paths...)
	}
	ok := false
	for _, path := range paths {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		ok = exec.CommandContext(ctx, lsregister, append(args[:len(args)-1:len(args)-1], path)...).Run() == nil || ok
		cancel()
	}
	return ok
}

// forgetBackupApps 盡力而為：只有該備份裡的 App 都取消登記成功才記為已處理，失敗（逾時、外接磁碟
// 尚未掛上）時下次背景更新再試。
func forgetBackupApps() {
	backups := filepath.Join(basePath(), "backups")
	dirs, err := filepath.Glob(filepath.Join(backups, "macos-*"))
	if err != nil || len(dirs) == 0 {
		return
	}
	marker := filepath.Join(backups, lsForgotten)
	b, _ := os.ReadFile(marker)
	done := strings.Fields(string(b))
	changed := false
	for _, dir := range dirs {
		name := filepath.Base(dir)
		if slices.Contains(done, name) {
			continue
		}
		ok := true
		for _, app := range backupBundles(dir) {
			ok = runLSRegister("-u", app) && ok
		}
		if !ok {
			continue
		}
		done = append(done, name)
		changed = true
	}
	if changed {
		slices.Sort(done)
		_ = os.WriteFile(marker, []byte(strings.Join(done, "\n")+"\n"), 0o600)
	}
}

// forgetStaleShells 把仍在執行的舊外殼所屬的 App（例如 backups 裡已記為處理過、卻仍被系統開啟的副本）
// 從 LaunchServices 取消登記，再登記目前安裝的 App，重新登入後系統改開新版（#98）。
func forgetStaleShells(apps []string) {
	installed := installedApp()
	for _, app := range apps {
		if app == installed {
			continue // 同一路徑的舊執行檔：App 已換新，重新登入即可
		}
		runLSRegister("-u", app)
		for _, nested := range []string{"Contents/Helpers/*.app", "Contents/Library/*.app"} {
			m, _ := filepath.Glob(filepath.Join(app, nested))
			for _, bundle := range m {
				runLSRegister("-u", bundle)
			}
		}
	}
	registerInstalledApp(installed)
}

// registerInstalledApp 讓 LaunchServices 以目前安裝的 App 為準（含內附的設定程式）。
func registerInstalledApp(app string) {
	if app != "" {
		runLSRegister("-f", "-R", app)
	}
}
