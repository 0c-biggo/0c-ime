package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// installedDataDir：收據記錄的版本目錄（同 spawnEngine 沒有核心指標時的做法）。
func installedDataDir(base string) string {
	b, err := os.ReadFile(receiptPath())
	if err != nil {
		return ""
	}
	var r struct {
		Directory string `json:"directory"`
	}
	if json.Unmarshal(b, &r) != nil || !strings.HasPrefix(r.Directory, "versions/") {
		return ""
	}
	return filepath.Join(base, filepath.FromSlash(r.Directory), "data")
}
