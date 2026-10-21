//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// macOS 的輸入法程式交接（#170）只讀文字與檔案的部分，放在這裡讓 Linux 上也跑得到單元測試；查詢與操作系統的
// 部分在 shell_handover_darwin.go。
//
// 0c.app 是 IMK 輸入法程式，系統在第一次用到時啟動、常駐到登出；安裝只換掉磁碟上的 App，執行中的那一份不會換新，
// 外殼的新功能要重新登入才生效。安裝、回復、解除安裝與排程喚起時，請本帳號執行中、不是目前安裝的那一份
// 0c.app（含 0.1.0-dev.21 以前的 Zuin.app）在使用者沒有組字、停手之後結束，再以不搶前景的方式（open -g -j）
// 啟動新的 App。做法同鼠鬚管安裝時結束舊的 Squirrel，但鼠鬚管與威注音都只在使用者自己執行安裝程式時做。
//
// 結束舊程式時，已經連著它的 App 不會自己換到新版：要等到焦點改變（切換 App、鎖定後解鎖、螢幕關閉後喚醒、
// 螢幕保護程式結束）才連上新的；在那之前按的第一個鍵會以英文字母直接交給 App，沒有先啟動新版時整批按鍵都會
// 漏成英文或被吃掉（#178，macOS 14／15／26 實測）。所以：
//   - 使用者自己安裝、回復（終端機、.pkg、設定程式「立即安裝」）時，使用者正看著安裝程式，之後回到原本的 App
//     就是一次焦點改變：停手後立即交接並啟動新版。
//   - 背景（排程、背景自動安裝）只在使用者離開時交接：這個帳號的螢幕已鎖定、螢幕保護程式或螢幕關閉中，或已切換
//     到別的使用者（macConsoleAway）。回來時的解鎖、喚醒就是焦點改變，一個字都不會漏。使用中停手多久都不交接，
//     舊程式搭新版引擎照常能打字，等下一次離開或重新登入。

// macShellHelper：同一個執行檔帶參數執行的是一次性工具（--activate-input-source、--update-agent、--update-notice
// 等），不是輸入法程式本身，不能結束。
func macShellHelper(args string) bool { return strings.Contains(args, " --") }

// underAny：path 在 roots 任一個目錄之下。
func underAny(path string, roots []string) bool {
	for _, root := range roots {
		if root != "" && strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// retirableMacShells 列出要結束的輸入法程式：App 在這個帳號自己的安裝位置（roots：Input Methods 與備份目錄）、
// 不是一次性工具（讀不到命令列的也不碰）、也不是目前安裝的那一份。current 是目前安裝的執行檔（解除安裝時為空，全部都要結束）；
// 同一路徑的程序以 isCurrent 核對是不是換新後的執行檔（inode），無法判斷時不碰。
func retirableMacShells(procs []macShellProcess, args map[string]string, roots []string, current string, isCurrent func(pid string) (bool, error)) []uiProcess {
	var out []uiProcess
	for _, p := range procs {
		cmdline, known := args[p.PID]
		if !known || macShellHelper(cmdline) || !underAny(p.Exe, roots) {
			continue
		}
		if current != "" && p.Exe == current {
			ok, err := isCurrent(p.PID)
			if err != nil || ok {
				continue
			}
		}
		pid, err := parsePID(p.PID)
		if err != nil {
			continue
		}
		out = append(out, uiProcess{PID: pid, Path: p.Exe})
	}
	return out
}

// awayCheckEvery：awayOnly 多久重新問一次使用者在不在（每次都要跑 ioreg）。retireProcesses 每 200 毫秒一輪、
// 每個程序各問一次停手多久，不必每次都重跑；晚一秒發現使用者回來也不會誤關，停手時間（idle）仍是每次即時讀的。
const awayCheckEvery = time.Second

// awayOnly 讓交接只在使用者離開時進行：人在的時候一律當成剛按過鍵（停手 0 秒），不會送出結束要求。
// 持續重新判斷（最多每 awayCheckEvery 一次）：等待中途使用者解鎖回來就停下。
func awayOnly(c uiControl, away func() bool) uiControl {
	idle, now := c.idle, c.now
	if now == nil {
		now = time.Now
	}
	var checked time.Time
	last := false
	c.idle = func() time.Duration {
		if t := now(); checked.IsZero() || t.Sub(checked) >= awayCheckEvery || t.Before(checked) {
			last, checked = away(), t
		}
		if !last {
			return 0
		}
		return idle()
	}
	return c
}

// handOverShells 請過期的輸入法程式結束；全部結束後呼叫 relaunch 啟動新版，縮短系統自己帶起新版之前的空窗。
// 還有舊程式沒結束時不啟動：同一個 bundle ID 的兩份 App 會搶同一個 IMK 連線名稱。
func handOverShells(c uiControl, pending func() []uiProcess, wait time.Duration, relaunch func()) (retired int, left []uiProcess) {
	retired, left = retireProcesses(c, pending, wait)
	if retired > 0 && len(left) == 0 && relaunch != nil {
		relaunch()
	}
	return retired, left
}

// macConsoleSessions 解析 `ioreg -n Root -d1` 輸出裡的 IOConsoleUsers：每個登入工作階段一個 key → value（去掉引號）。
// 值裡可能有逗號、大括號（例如全名），引號內的字元一律照抄；巢狀的值整段保留、不再展開。
func macConsoleSessions(ioreg string) []map[string]string {
	_, rest, ok := strings.Cut(ioreg, `"IOConsoleUsers" = (`)
	if !ok {
		return nil
	}
	var (
		out     []map[string]string
		cur     map[string]string
		tok     strings.Builder
		key     string
		haveKey bool
		depth   int // 0：陣列裡、字典之間；1：字典的 key=value 層；更深：巢狀的值
		quoted  bool
	)
	pair := func() {
		if haveKey && cur != nil {
			cur[key] = strings.Trim(strings.TrimSpace(tok.String()), `"`)
		}
		tok.Reset()
		key, haveKey = "", false
	}
	for _, r := range rest {
		if quoted {
			if r == '"' {
				quoted = false
			}
			tok.WriteRune(r)
			continue
		}
		switch {
		case r == '"':
			quoted = true
			tok.WriteRune(r)
		case r == '{' || r == '(':
			depth++
			if depth == 1 {
				cur, key, haveKey = map[string]string{}, "", false
				tok.Reset()
			} else {
				tok.WriteRune(r)
			}
		case (r == '}' || r == ')') && depth == 0:
			return out // 陣列結束
		case r == '}' || r == ')':
			if depth == 1 {
				pair()
				out = append(out, cur)
				cur = nil
			} else {
				tok.WriteRune(r)
			}
			depth--
		case r == '=' && depth == 1 && !haveKey:
			key, haveKey = strings.Trim(strings.TrimSpace(tok.String()), `"`), true
			tok.Reset()
		case r == ',' && depth == 1:
			pair()
		case r == '\n' && depth == 0:
			return out
		case depth >= 1:
			tok.WriteRune(r)
		}
	}
	return nil // 沒有正常結束：格式看不懂
}

// macConsoleAway：uid 這個帳號的使用者是不是離開了電腦——自己的工作階段標示螢幕已鎖定（CGSSessionScreenIsLocked：
// 鎖定畫面、螢幕保護程式、螢幕關閉時都會設，macOS 15 實測），或不在主控台（快速切換到別的使用者）。
// 找不到自己的工作階段、或輸出看不懂時回 false：無法確認就當成人還在，不交接。
func macConsoleAway(ioreg string, uid int) bool {
	for _, s := range macConsoleSessions(ioreg) {
		if s["kCGSSessionUserIDKey"] != strconv.Itoa(uid) {
			continue
		}
		return s["CGSSessionScreenIsLocked"] == "Yes" || s["kCGSSessionOnConsoleKey"] == "No"
	}
	return false
}

func parsePID(s string) (uint32, error) {
	pid, err := strconv.ParseUint(s, 10, 32)
	return uint32(pid), err
}

// macNoShiftSince 是 macOS 中英切換鍵預設改成「不用 Shift」的第一個版本（#167）；更早的版本預設右 Shift。
const macNoShiftSince = "0.2.1"

// macKeepsRightShift：這次安裝是不是從右 Shift 還是預設的版本升級。prev 是升級前的收據（沒有收據時為 nil），
// appExisted 表示升級前已有 App（沒有收據的原型、或收據遺失）。全新安裝不寫，採 macOS 預設。
func macKeepsRightShift(prev *update.MacReceipt, appExisted bool) bool {
	if prev == nil {
		return appExisted
	}
	if prev.Version == "" || !update.ValidVersion(prev.Version) {
		return true
	}
	order, err := update.CompareVersion(prev.Version, macNoShiftSince)
	return err == nil && order < 0
}

// macUpgradedFromRightShift 在安裝完成之後判斷（背景更新由舊版更新器安裝時，新版的程式碼要事後補做）：
// 備份裡的上一個安裝是 0.2.1 以前的版本。不看目前的版本：0.2.1 的候選版（0.2.1-rc 等）也已改成「不用 Shift」；
// 回復到舊版時，上一個安裝是較新的版本，不會寫入。
func macUpgradedFromRightShift(support string) bool {
	current, err := readMacReceiptFile(filepath.Join(support, "install-receipt.json"))
	if err != nil || current == nil || !current.Active || current.Previous == "" || !update.SafeName(current.Previous) {
		return false
	}
	dir := filepath.Join(support, filepath.FromSlash(current.Previous))
	prev, err := readMacReceiptFile(filepath.Join(dir, "previous.json"))
	if err != nil {
		return false // 備份已被清掉或讀不到：無法判斷就不改設定
	}
	existed := false
	for _, name := range []string{update.MacApp, update.LegacyMacApp} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			existed = true
		}
	}
	return macKeepsRightShift(prev, existed)
}

// readMacReceiptFile 讀收據；檔案內容是 null（全新安裝的備份）時回傳 nil。
func readMacReceiptFile(path string) (*update.MacReceipt, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r *update.MacReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return r, nil
}
