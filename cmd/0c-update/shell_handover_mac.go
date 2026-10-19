//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"git.biggo.com/0c/ime/internal/update"
)

// macOS 的輸入法程式交接（#170）只讀文字與檔案的部分，放在這裡讓 Linux 上也跑得到單元測試；查詢與操作系統的
// 部分在 shell_handover_darwin.go。
//
// 0c.app 是 IMK 輸入法程式，系統在第一次用到時啟動、常駐到登出；安裝只換掉磁碟上的 App，執行中的那一份不會換新，
// 外殼的新功能要重新登入才生效。安裝、回復、解除安裝與每次排程喚起時，請本帳號執行中、不是目前安裝的那一份
// 0c.app（含 0.1.0-dev.21 以前的 Zuin.app）在使用者沒有組字、停手之後結束；新版不由這裡啟動，系統下一次需要
// 靈犀注音時（切換輸入法或在文字欄位打字）照原本的方式從 ~/Library/Input Methods 啟動新的 App。做法同鼠鬚管
// 安裝時結束舊的 Squirrel，再由系統重新啟動。

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
