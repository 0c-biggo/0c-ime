package main

import (
	"os"
	"path/filepath"
)

// 與 macOS 輸入法 App 傳給引擎的個人資料目錄相同。
func defaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Application Support/0c")
}
