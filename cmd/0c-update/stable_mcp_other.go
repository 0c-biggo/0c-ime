//go:build !windows

package main

// refreshStableMCP：Linux 的 ~/.local/bin/0c-mcp 由套件的 install.py 安裝與移除，macOS 的在輸入法 App 裡，
// 隨 App 整個更新；只有 Windows 的版本目錄會換位置，需要另一份穩定副本。
func refreshStableMCP(string) {}
