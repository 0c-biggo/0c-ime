package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 0c-mcp 是只支援 stdio 的 AI 工具用的轉接（#126）。使用者把它的路徑貼進 AI 工具的設定檔後就不會再改，
// 所以路徑不能隨版本變動：Linux 由 install.py 放在 ~/.local/bin/0c-mcp，macOS 在原地更新的輸入法 App 裡
// （Contents/MacOS/0c-mcp），Windows 的版本目錄 versions\<版本> 每次更新都換，於是比照穩定更新器在
// %LOCALAPPDATA%\Zuin\bin\0c-mcp.exe 放一份穩定副本（設定程式 platforms/settings 的 planned_mcp_path 填這裡）。

func mcpName() string {
	if runtime.GOOS == "windows" {
		return "0c-mcp.exe"
	}
	return "0c-mcp"
}

// stableMCP 是 Windows 上 AI 工具設定填的 0c-mcp 位置，與穩定更新器同一個目錄。
func stableMCP(base string) string { return filepath.Join(base, "bin", mcpName()) }

// currentMCP 是目前版本的 0c-mcp 與安裝時核對過的雜湊。有引擎指標（核心更新）而核心目錄記錄了它時取核心目錄的；
// 核心目錄沒有它（較舊的更新器準備的核心目錄只複製引擎、更新器與字典）時退回完整安裝收據的那一份：
// 與穩定更新器不同，舊一點的轉接照樣能用，總比讓 AI 工具的設定突然失效好。都沒有時為空字串。
func currentMCP(base, receipt string) (string, string) {
	name := mcpName()
	if dir, ok := enginedir.Dir(base); ok {
		if files, err := enginedir.ReadFiles(dir); err == nil && files[name] != "" {
			return filepath.Join(dir, name), files[name]
		}
	}
	return installedFromReceipt(receipt, base, name)
}

// syncStableMCP 讓穩定副本 stable 與目前版本的 0c-mcp（source，安裝時記錄的雜湊 want）一致：不同就換上，
// 目前版本沒有附 0c-mcp（回復到舊版、解除安裝）就移走，設定程式才會照實註明「這個版本還沒附 0c-mcp」。
// 來源先依安裝紀錄核對，被改過或不完整就不換。回傳有沒有變更。
//
// AI 工具可能正在執行它：Windows 上執行中的檔案不能覆寫或刪除，但可以改名，所以換檔沿用 replaceUpdater，
// 移走時刪不掉就改名成 .old-*，留給下一次清除。
func syncStableMCP(source, want, stable string) (bool, error) {
	if source == "" || want == "" {
		return retireFile(stable)
	}
	if have, err := update.Hash(stable); err == nil && have == want {
		removeOldCopies(stable)
		return false, nil
	}
	got, err := update.Hash(source)
	if err != nil {
		return false, err
	}
	if got != want {
		return false, fmt.Errorf("目前版本的 %s 與安裝紀錄不符", filepath.Base(source))
	}
	if err = replaceUpdater(source, stable); err != nil {
		return false, err
	}
	return true, nil
}

// retireFile 移除不該再存在的穩定副本；仍在執行而刪不掉時改名，原路徑立即空出來。
func retireFile(path string) (bool, error) {
	removeOldCopies(path)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s 不是一般檔案", path)
	}
	if err = os.Remove(path); err == nil {
		return true, nil
	}
	if err = os.Rename(path, fmt.Sprintf("%s.old-%d", path, time.Now().UnixNano())); err != nil {
		return false, err
	}
	return true, nil
}
