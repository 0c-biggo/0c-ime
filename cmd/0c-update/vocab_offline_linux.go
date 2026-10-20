package main

import (
	"os"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

// installedDataDir：官網安裝的字典；沒有時是系統安裝的字典（#181，與外殼找引擎的規則相同，官網安裝優先）。
func installedDataDir(base string) string {
	personal := filepath.Join(base, "data")
	if _, err := os.Stat(filepath.Join(personal, "manifest.json")); err == nil {
		return personal
	}
	if system := installs.Default().SystemData(); system != "" {
		if _, err := os.Stat(filepath.Join(system, "manifest.json")); err == nil {
			return system
		}
	}
	return personal
}
