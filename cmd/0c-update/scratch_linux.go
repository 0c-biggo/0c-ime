package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// scratchRoot 是下載與解開套件用的暫存根目錄。Linux 的 /tmp 常是 tmpfs，有大小或使用者配額（更新會以
// disk quota exceeded 失敗），所以放在使用者資料目錄；建不起來時回傳空字串，改用系統暫存目錄。
// 這裡不像 /tmp 會在重開機時清空，所以順便清掉被中斷的更新留下、超過一天的暫存。
func scratchRoot() string {
	dir := filepath.Join(basePath(), "update", "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	update.RemoveStale(dir, 24*time.Hour, "0c-download-", "0c-install-", "0c-update-")
	return dir
}
