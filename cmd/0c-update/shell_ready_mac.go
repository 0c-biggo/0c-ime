//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// macOS 外殼生效的判斷只讀文字與檔案，放在這裡讓 Linux 上也跑得到單元測試；查詢系統的部分在 shell_ready_darwin.go。

// Same-session engine restarts cannot prove that the new IMK shell is active.
// ASID belongs to the GUI domain; unlocking or switching users keeps it intact.
// 開機後 ASID 從同一個起點重新編號，重開機後的第一個登入可能拿到與安裝時相同的 ASID（#98），
// 所以開機時間晚於收據寫入（安裝完成）也算跨過登入邊界；boot 為零值表示無法取得。
// errNoLoginBoundary：收據或目前的工作階段沒有 ASID，又沒有重新開機，無法判斷是否跨過登入邊界。
var errNoLoginBoundary = errors.New("無法確認安裝與登入的工作階段邊界")

func macShellBoundary(receipt, version string, current uint64, boot time.Time) (bool, error) {
	b, err := os.ReadFile(receipt)
	if err != nil {
		return false, err
	}
	var r update.MacReceipt
	if err := json.Unmarshal(b, &r); err != nil {
		return false, err
	}
	if !r.Active || r.Version != version || r.App != update.MacApp {
		return false, fmt.Errorf("新版外殼收據尚未就緒")
	}
	// 收據只在安裝、回復與移除時改寫，修改時間就是安裝完成的時間。
	if info, err := os.Stat(receipt); err == nil && !boot.IsZero() && boot.After(info.ModTime()) {
		return true, nil
	}
	if r.SessionASID == 0 || current == 0 {
		return false, errNoLoginBoundary
	}
	return r.SessionASID != current, nil
}

func mappedMacShell(out, executable string, inode uint64) bool {
	// lsof defaults to newline-delimited field records, including the f field.
	// Path and inode must belong to one txt record, not to different mappings.
	for _, record := range strings.Split(out, "ftxt\n")[1:] {
		fields := strings.SplitN(record, "\nf", 2)[0]
		var path string
		var actual uint64
		for _, line := range strings.Split(fields, "\n") {
			if strings.HasPrefix(line, "n") {
				path = line[1:]
			} else if strings.HasPrefix(line, "i") {
				actual, _ = strconv.ParseUint(line[1:], 10, 64)
			}
		}
		if path == executable && actual == inode && inode != 0 {
			return true
		}
	}
	return false
}

// macShellProcess 是正在執行的靈犀注音外殼（0c.app，或 0.1.0-dev.21 以前的 Zuin.app），不論在哪個位置。
type macShellProcess struct {
	PID string
	Exe string // 執行檔完整路徑
	App string // 所屬的 .app
}

// macShellProcesses 從 `ps -o pid=,comm=` 的輸出找出外殼程序；macOS 的 comm 是執行檔完整路徑。
func macShellProcesses(ps string) []macShellProcess {
	var out []macShellProcess
	for _, line := range strings.Split(ps, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		exe := strings.Join(fields[1:], " ")
		for _, suffix := range []string{".app/Contents/MacOS/0c", ".app/Contents/MacOS/Zuin"} {
			if strings.HasSuffix(exe, suffix) {
				app := strings.TrimSuffix(exe, suffix[len(".app"):])
				out = append(out, macShellProcess{PID: fields[0], Exe: exe, App: app})
				break
			}
		}
	}
	return out
}

// staleMacShells 列出不是目前安裝的外殼：其他位置的副本（例如 backups 裡的舊 App），或同一路徑但仍對應
// 換掉前的舊執行檔。current 判斷程序是否對應目前安裝的執行檔（以 inode 核對），無法判斷時回傳錯誤。
// 一個外殼都沒有（還沒有 App 用到輸入法）不是舊外殼的證據：下次載入時系統開的就是新安裝的 App。
func staleMacShells(procs []macShellProcess, installed string, current func(pid string) (bool, error)) ([]string, error) {
	var stale []string
	for _, p := range procs {
		if !strings.HasSuffix(p.Exe, "/Contents/MacOS/0c") {
			continue // 0.1.0-dev.21 以前的 Zuin.app 是另一個輸入來源，不是這次更新的外殼
		}
		if p.Exe == installed {
			ok, err := current(p.PID)
			if err != nil {
				return nil, err
			}
			if ok {
				continue
			}
		}
		stale = append(stale, p.App)
	}
	return stale, nil
}
