//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// shellDiagnostics 回報 macOS 版本與正在執行的輸入法是哪一份 App、哪個版本，放進 status -json 與
// 設定程式匯出的狀態摘要。使用者不必開終端機，就看得出是否開到備份裡的舊外殼（#77）。
func shellDiagnostics() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := map[string]any{}
	if v, err := exec.CommandContext(ctx, "/usr/bin/sw_vers", "-productVersion").Output(); err == nil {
		out["os_version"] = strings.TrimSpace(string(v))
	}
	// 已安裝的 App 被改動（例如使用者改過程式並重新簽章、防毒軟體動過檔案）時列出哪些檔案，更新照常進行（#83）。
	if changed, err := update.CheckMac(paths()); err != nil {
		out["app_check"] = err.Error()
	} else if changed != nil {
		out["app_check"] = changed.Error()
	}
	installed := filepath.Join(installedApp(), "Contents/MacOS/0c")
	var inode uint64
	if info, err := os.Stat(installed); err == nil {
		inode = info.Sys().(*syscall.Stat_t).Ino
	}
	ps, err := exec.CommandContext(ctx, "/bin/ps", "-U", strconv.Itoa(os.Getuid()), "-o", "pid=,comm=").Output()
	if err != nil {
		return out
	}
	processes := []map[string]any{}
	for _, proc := range macShellProcesses(string(ps)) {
		p := map[string]any{"app": proc.App, "version": bundleBuildVersion(ctx, proc.App)}
		if lsof, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", proc.PID, "-d", "txt", "-F", "fni").Output(); err == nil {
			p["installed_copy"] = mappedMacShell(string(lsof), installed, inode)
		}
		processes = append(processes, p)
	}
	out["input_method_processes"] = processes
	asid, _ := macAquaSession(ctx)
	if r, err := readMacReceiptVersion(); err == nil {
		ready, _ := macShellBoundary(receiptPath(), r, asid, macBootTime())
		out["new_login_since_install"] = ready
	}
	return out
}

// bundleBuildVersion 讀 App 的完整版本：dev.59 起打包時寫入 ZCBuildVersion；更早的只有 CFBundleShortVersionString。
func bundleBuildVersion(ctx context.Context, app string) string {
	plist := filepath.Join(app, "Contents/Info.plist")
	for _, key := range []string{"ZCBuildVersion", "CFBundleVersion"} {
		if v, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plist).Output(); err == nil {
			if s := strings.TrimSpace(string(v)); s != "" {
				if key == "CFBundleVersion" {
					return "bundle " + s
				}
				return s
			}
		}
	}
	return ""
}

func readMacReceiptVersion() (string, error) {
	b, err := os.ReadFile(receiptPath())
	if err != nil {
		return "", err
	}
	var r update.MacReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	return r.Version, nil
}
