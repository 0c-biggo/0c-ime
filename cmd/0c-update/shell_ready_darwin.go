//go:build darwin

package main

import (
	"context"
	"errors"
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

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
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

// ShellReady 確認沒有仍在執行的舊外殼。還沒有 App 用到輸入法、外殼與引擎都沒在跑時也算生效：之後載入的就是
// 新安裝的 App，不能一直要求重新登入（#98）。使用者自己安裝時、或之後使用者離開電腦時的排程喚起，會請舊的輸入法程式
// 結束（#170、#178），同一個登入工作階段裡也能核對；安裝時記下要重新登入（輸入來源還沒登記、簽章改變要按允許）的才要跨過登入邊界。
// 跨過登入邊界後執行中的是備份裡的舊副本或換掉前的舊執行檔時回 ErrOldShellLoaded；同一個工作階段裡是還沒結束的
// 舊程式，回 ErrShellRetiring。兩種都把該副本從 LaunchServices 取消登記。
func (p *system) ShellReady(ctx context.Context, version string) (bool, error) {
	asid, _ := macAquaSession(ctx) // 取不到時為 0，由開機時間判斷；兩者都無法判斷時 macShellBoundary 回報
	crossed, err := macShellBoundary(receiptPath(), version, asid, macBootTime())
	if err != nil && !errors.Is(err, errNoLoginBoundary) {
		return false, err
	}
	if relogin := macReloginRequired(basePath()); !crossed && relogin {
		return false, err
	}
	// 先確保備份裡的舊 App 不會再被系統開啟（dev.58 以前的更新器不會取消登記）。
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
		if !crossed {
			return false, fmt.Errorf("%w：%s", autoupdate.ErrShellRetiring, strings.Join(stale, "、"))
		}
		return false, fmt.Errorf("%w：%s", autoupdate.ErrOldShellLoaded, strings.Join(stale, "、"))
	}
	return true, nil
}
