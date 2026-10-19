//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

var aquaASID = regexp.MustCompile(`(?m)^\s*asid = (\d+)\s*$`)

func macAquaSession(ctx context.Context) (uint64, error) {
	out, err := exec.CommandContext(ctx, "/bin/launchctl", "print", fmt.Sprintf("gui/%d", os.Getuid())).Output()
	if err != nil {
		return 0, fmt.Errorf("無法確認 Aqua 工作階段：%w", err)
	}
	return parseAquaSession(string(out))
}

func parseAquaSession(out string) (uint64, error) {
	m := aquaASID.FindStringSubmatch(out)
	if len(m) != 2 {
		return 0, fmt.Errorf("Aqua 工作階段沒有 ASID")
	}
	id, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("Aqua ASID 無效")
	}
	return id, nil
}

// ShellLoadProven：ShellReady 核對了執行中的輸入法程序都對應新版執行檔（或還沒有任何程序），不只是登入邊界。
func (p *system) ShellLoadProven() bool { return true }

// macBootTime 是這次開機的時間；取不到時為零值，邊界改由 ASID 判斷。
func macBootTime() time.Time {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil || tv.Sec <= 0 {
		return time.Time{}
	}
	return time.Unix(tv.Unix())
}

// ShellReady 在跨過登入邊界（重新登入或重新開機）後，確認沒有仍在執行的舊外殼。還沒有 App 用到輸入法、
// 外殼與引擎都沒在跑時也算生效：之後載入的就是新安裝的 App，不能一直要求重新登入（#98）。
// 執行中的是備份裡的舊副本或換掉前的舊執行檔時回 ErrOldShellLoaded，並把該副本從 LaunchServices 取消登記。
func (p *system) ShellReady(ctx context.Context, version string) (bool, error) {
	asid, _ := macAquaSession(ctx) // 取不到時為 0，由開機時間判斷；兩者都無法判斷時 macShellBoundary 回報
	ready, err := macShellBoundary(receiptPath(), version, asid, macBootTime())
	if !ready || err != nil {
		return false, err
	}
	// 新的登入工作階段：先確保備份裡的舊 App 不會再被系統開啟（dev.58 以前的更新器不會取消登記）。
	forgetBackupApps()
	executable := filepath.Join(installedApp(), "Contents/MacOS/0c")
	info, err := os.Stat(executable)
	if err != nil {
		return false, err
	}
	inode := info.Sys().(*syscall.Stat_t).Ino
	out, err := exec.CommandContext(ctx, "/bin/ps", "-U", strconv.Itoa(os.Getuid()), "-o", "pid=,comm=").Output()
	if err != nil {
		return false, err
	}
	stale, err := staleMacShells(macShellProcesses(string(out)), executable, func(pid string) (bool, error) {
		out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", pid, "-d", "txt", "-F", "fni").Output()
		if err != nil {
			return false, err
		}
		return mappedMacShell(string(out), executable, inode), nil
	})
	if err != nil {
		return false, err
	}
	if len(stale) > 0 {
		forgetStaleShells(stale)
		return false, fmt.Errorf("%w：%s", autoupdate.ErrOldShellLoaded, strings.Join(stale, "、"))
	}
	return true, nil
}
