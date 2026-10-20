package main

import (
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/installs"
)

// activeDataDir 是引擎實際用的字典（models 與 vocab 都用它，pairing.lock 也從它旁邊讀）：規則與外殼啟動引擎相同
// （installs.Layout.Server，#181）。
func activeDataDir(string) (string, bool) { return activeDataIn(installs.Default()) }

// activeDataIn：有官網安裝的引擎（~/.local/bin/0c-server）時用它的字典，核心更新後是 engine.json 指的版本目錄；
// 否則用系統安裝的字典。只看字典在不在會選錯：官網版解除安裝保留 ~/.local/share/zuin/data 與 engine.json，
// 之後改裝系統套件時，引擎用的是 share/0c/data。兩份都沒有時 ok 為 false，沿用以前的找法（開發、測試）。
func activeDataIn(l installs.Layout) (string, bool) {
	if in, ok := l.Personal(); ok {
		return in.Data, true
	}
	if in, ok := l.System(); ok {
		return in.Data, true
	}
	return "", false
}

// installedDataDir：沒有找到任何安裝時，引擎不帶 -data 啟動用的預設位置。
func installedDataDir(base string) string { return filepath.Join(base, "data") }
