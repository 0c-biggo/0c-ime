//go:build windows

package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// scratchRoot 是解開套件的暫存根目錄：%LOCALAPPDATA%\Zuin\update\stage（#292）。不放 %TEMP%：在 %TEMP% 解開並
// 執行（尤其是提權執行）程式是端點防護常見的訊號（#291 的報告），使用者清暫存檔也不會清掉安裝到一半的套件。
// 路徑比 %TEMP% 長十來個字元，最長的檔案仍可能超過 260 字元；Go 的檔案操作自動改用 \\?\ 長路徑，從這裡執行的只有
// 0c-update 與 0c-server（-check-data），不受影響。建不起來時回傳空字串，改用系統暫存目錄。
func scratchRoot() string {
	dir := filepath.Join(basePath(), "update", "stage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	update.RemoveStale(dir, 24*time.Hour, "0c-download-", "0c-install-", "0c-update-")
	return dir
}
