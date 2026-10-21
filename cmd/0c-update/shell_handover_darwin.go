//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/settings"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 輸入法程式（0c.app）的交接，說明見 shell_handover_mac.go。
//
// 使用者自己安裝、回復、解除安裝時停手 2 秒就交接，最多等 10 秒；排程喚起（30 秒）與背景自動安裝只在使用者離開時
// （macUserAway）交接，同樣要沒有組字、停手 2 秒（解鎖時輸入密碼的按鍵不算組字，但也不在那一刻收掉）。
const (
	uiWaitInstall = 10 * time.Second
	uiWaitTask    = 60 * time.Second
	uiWaitAuto    = 30 * time.Second
	macQuiet      = 2 * time.Second
)

// handOverUI：每次排程喚起時補做交接。0.2.0 以前的更新器在背景安裝新版時不會結束舊的輸入法程式，
// 也不會替升級的人保留右 Shift（#167）；新的穩定副本下一次喚起時在這裡補上。交接只在使用者離開時做（#178）。
func handOverUI(wait time.Duration) {
	base := basePath()
	if keepRightShiftAfterUpgrade(base) {
		markSettingsApply(base)
	}
	if !macReloginRequired(base) {
		if current := currentShellExe(); current != "" && fileExists(current) {
			retireMacShells(current, wait, true)
		}
	}
	applyPendingSettings(base)
}

// stopUI（解除安裝）：結束這個帳號執行中的靈犀注音輸入法程式。
func stopUI(wait time.Duration) { retireMacShells("", wait, false) }

// retireMacShells 請過期的輸入法程式在沒有組字、使用者停手之後結束；whenAway 時只在使用者離開時（背景交接）。
// current 不為空時，全部結束後以不搶前景的方式啟動 current 所在的 App。回傳結束了幾個，以及到期時仍在執行的。
func retireMacShells(current string, wait time.Duration, whenAway bool) (int, []uiProcess) {
	pending := func() []uiProcess { return listMacShells(current) }
	todo := pending()
	if len(todo) == 0 {
		return 0, nil
	}
	if whenAway && !userAway() {
		return 0, todo // 使用中：不交接，也不在這裡空等
	}
	c := macShellControl()
	c.quiet = macQuiet
	if whenAway {
		c = awayOnly(c, userAway)
	}
	var relaunch func()
	if current != "" {
		app := filepath.Dir(filepath.Dir(filepath.Dir(current))) // <App>/Contents/MacOS/<執行檔>
		relaunch = func() { launchMacShell(app) }
	}
	return handOverShells(c, pending, wait, relaunch)
}

// listMacShells、macShellControl、userAway、launchMacShell 測試替換用：go test 不結束、不啟動這台機器上真的輸入法程式
// （isolate_darwin_test.go）。
var (
	listMacShells   = runningMacShells
	macShellControl = darwinShellControl
	userAway        = macUserAway
	launchMacShell  = openMacShell
)

// ioregTimeout：ioreg 平常幾十毫秒；卡住時當成查不到（人還在、沒停手），不讓交接或排程一直等下去。
const ioregTimeout = 5 * time.Second

// macUserAway：這個帳號的使用者離開了電腦（螢幕鎖定、螢幕保護程式、螢幕關閉，或切換到別的使用者），見 macConsoleAway。
func macUserAway() bool {
	ctx, cancel := context.WithTimeout(context.Background(), ioregTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-n", "Root", "-d1").Output()
	if err != nil {
		return false
	}
	return macConsoleAway(string(out), os.Getuid())
}

// openMacShell 以不搶前景、不顯示的方式啟動新的輸入法程式（open -g -j）。系統本來也會在下一次需要時啟動它，
// 先啟動可以讓焦點一改變就連上新版，不必等到按鍵（那個按鍵會以英文字母交給 App）。
func openMacShell(app string) {
	if out, err := exec.Command("/usr/bin/open", "-g", "-j", app).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "0c-update: 無法啟動新的輸入法程式（%s）：%v\n", strings.TrimSpace(string(out)), err)
	}
}

func darwinShellControl() uiControl {
	return uiControl{
		idle:   macShellQuiet,
		close:  terminateShell,
		exited: waitShellExit,
		sleep:  time.Sleep,
		now:    time.Now,
	}
}

// currentShellExe 是目前安裝的輸入法執行檔；沒有啟用中的安裝時為空。
func currentShellExe() string {
	r, err := readMacReceiptFile(receiptPath())
	if err != nil || r == nil || !r.Active {
		return ""
	}
	name, exe := update.MacApp, "0c"
	if r.App == "" || r.App == update.LegacyMacApp {
		name, exe = update.LegacyMacApp, "Zuin"
	}
	return filepath.Join(paths().InputMethods, name, "Contents/MacOS", exe)
}

// macShellRoots 是這個帳號自己的安裝位置：Input Methods 與備份目錄（.12 的備份目錄是連結，連實際位置一起算）。
func macShellRoots() []string {
	p := paths()
	backups := filepath.Join(p.Support, "backups")
	roots := []string{p.InputMethods, backups}
	if real, err := filepath.EvalSymlinks(backups); err == nil && real != backups {
		roots = append(roots, real)
	}
	return roots
}

func runningMacShells(current string) []uiProcess {
	uid := strconv.Itoa(os.Getuid())
	// 先取參數再取執行檔：兩次之間才出現的程序沒有參數紀錄，一律不碰。
	argsOut, err := exec.Command("/bin/ps", "-U", uid, "-o", "pid=,args=").Output()
	if err != nil {
		return nil
	}
	commOut, err := exec.Command("/bin/ps", "-U", uid, "-o", "pid=,comm=").Output()
	if err != nil {
		return nil
	}
	var inode uint64
	if current != "" {
		info, err := os.Stat(current)
		if err != nil {
			return nil
		}
		inode = info.Sys().(*syscall.Stat_t).Ino
	}
	return retirableMacShells(macShellProcesses(string(commOut)), psArgs(string(argsOut)), macShellRoots(), current, func(pid string) (bool, error) {
		out, err := exec.Command("/usr/sbin/lsof", "-a", "-p", pid, "-d", "txt", "-F", "fni").Output()
		if err != nil {
			return false, err
		}
		return mappedMacShell(string(out), current, inode), nil
	})
}

// psArgs 把 `ps -o pid=,args=` 的輸出整理成 PID → 完整命令列。
func psArgs(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		pid, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(pid); err == nil {
			m[pid] = strings.TrimSpace(rest)
		}
	}
	return m
}

// macShellQuiet：使用者多久沒有按鍵、移動滑鼠；任何一個引擎正在組字（或無法確認）時為 0。
// 組字狀態在引擎裡：輸入法程式畫的組字區與候選窗都來自引擎的快照。
func macShellQuiet() time.Duration {
	home, _ := os.UserHomeDir()
	for _, name := range []string{"0c", "Zuin"} {
		sock := filepath.Join(home, "Library/Caches", name, "engine.sock")
		if _, err := macEngineIdle(func(timeout time.Duration) (net.Conn, error) { return net.DialTimeout("unix", sock, timeout) }); err != nil {
			return 0
		}
	}
	if idle, ok := hidIdleTime(); ok {
		return idle
	}
	return 0
}

// terminateShell 送 SIGTERM。0.2.0 以前的 App 沒有處理，立即結束；之後的 App 等到沒有組字、停手才結束
// （platforms/macos/Sources/Retire.swift）。送出前再核對一次 PID 仍是同一個執行檔，避免 PID 被重用。
func terminateShell(p uiProcess) error {
	if processExe(p.PID) != p.Path {
		return fmt.Errorf("程序已結束")
	}
	return syscall.Kill(int(p.PID), syscall.SIGTERM)
}

func waitShellExit(p uiProcess, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if err := syscall.Kill(int(p.PID), 0); errors.Is(err, syscall.ESRCH) || processExe(p.PID) != p.Path {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func processExe(pid uint32) string {
	out, err := exec.Command("/bin/ps", "-p", strconv.FormatUint(uint64(pid), 10), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// 安裝後要重新登入的情況：輸入來源還沒登記好（deferInputSource 記下、登入後補做），或簽章身分改變、要等使用者
// 按「允許」。這時舊的輸入法程式照常能打字，不結束它；新版在重新登入後載入。
func reloginMarker(base string) string { return filepath.Join(base, "update", "relogin-required") }

func markRelogin(base string) {
	asid, _ := macAquaSession(context.Background())
	_ = os.MkdirAll(filepath.Dir(reloginMarker(base)), 0o700)
	_ = os.WriteFile(reloginMarker(base), []byte(strconv.FormatUint(asid, 10)+"\n"), 0o600)
}

// macReloginRequired：還在安裝時的同一個登入工作階段、且安裝時記下要重新登入。跨過登入邊界（ASID 不同或
// 重新開機）後清掉記號。
func macReloginRequired(base string) bool {
	if fileExists(inputSourceMarker(base)) {
		return true
	}
	b, err := os.ReadFile(reloginMarker(base))
	if err != nil {
		return false
	}
	info, err := os.Stat(reloginMarker(base))
	if err != nil {
		return false
	}
	stored, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	current, _ := macAquaSession(context.Background())
	if boot := macBootTime(); (!boot.IsZero() && boot.After(info.ModTime())) || (stored != 0 && current != 0 && stored != current) {
		os.Remove(reloginMarker(base))
		return false
	}
	return true
}

func settingsFile(base string) string { return settings.Path(filepath.Join(base, "user.json")) }

// keepRightShift（#167）：升級的人保留右 Shift。只在設定檔沒有記下切換鍵時寫入，回報是否寫入。
func keepRightShift(base string) bool {
	wrote := false
	err := autoupdate.WithLock(base, "settings-write.lock", func() error {
		var err error
		wrote, err = settings.KeepToggleKey(settingsFile(base), settings.ToggleRight)
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 無法保留右 Shift 切換中英：", err)
	}
	if wrote {
		fmt.Println("保留原本的中英切換鍵：單按右 Shift（可在設定程式「輸入設定」改成「不用 Shift」）。")
	}
	return wrote
}

// keepRightShiftAfterUpgrade 依收據與備份判斷剛完成的安裝是不是從右 Shift 還是預設的版本升級，是的話保留右 Shift。
// 每次安裝只判斷一次（記下收據的備份位置）：之後使用者把設定檔整個清掉，也不會再寫回來。
func keepRightShiftAfterUpgrade(base string) bool {
	r, err := readMacReceiptFile(receiptPath())
	if err != nil || r == nil || r.Previous == "" {
		return false
	}
	marker := filepath.Join(base, "update", "toggle-key-checked")
	if b, _ := os.ReadFile(marker); strings.TrimSpace(string(b)) == r.Previous {
		return false
	}
	wrote := macUpgradedFromRightShift(base) && keepRightShift(base)
	_ = os.MkdirAll(filepath.Dir(marker), 0o700)
	_ = os.WriteFile(marker, []byte(r.Previous+"\n"), 0o600)
	return wrote
}

// 事後補寫設定時，執行中的引擎是用舊設定啟動的：記下來，沒有組字時請它交接，下一個按鍵以新設定啟動。
func settingsApplyMarker(base string) string { return filepath.Join(base, "update", "settings-apply") }

func markSettingsApply(base string) {
	_ = os.MkdirAll(filepath.Dir(settingsApplyMarker(base)), 0o700)
	_ = os.WriteFile(settingsApplyMarker(base), nil, 0o600)
}

func applyPendingSettings(base string) {
	if !fileExists(settingsApplyMarker(base)) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, running, err := autoupdate.Health(dialEngine); err == nil && !running {
		os.Remove(settingsApplyMarker(base))
		return
	}
	if err := autoupdate.Drain(ctx, dialEngine, 15*time.Second); err != nil {
		return // 正在組字：下一次喚起再試
	}
	os.Remove(settingsApplyMarker(base))
	ensureEngineService(base)
}

// hidIdleTime 是使用者多久沒有按鍵或移動滑鼠（IOHIDSystem 的 HIDIdleTime）。
func hidIdleTime() (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), ioregTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-d", "4").Output()
	if err != nil {
		return 0, false
	}
	if m := hidIdle.FindSubmatch(out); m != nil {
		if ns, err := strconv.ParseInt(string(bytes.TrimSpace(m[1])), 10, 64); err == nil {
			return time.Duration(ns), true
		}
	}
	return 0, false
}

// shellPendingAfterInstall：舊的輸入法程式在安裝時（使用者自己安裝）或使用者離開時（背景）結束，不必重新登入；
// 只有要重新登入的情況（見 macReloginRequired）才算待生效，其餘由協調器以 ShellReady 核對。
func shellPendingAfterInstall() bool { return macReloginRequired(basePath()) }
