//go:build !windows

package main

// listInputMethod：Linux 與 macOS 在安裝流程裡各自把輸入法加入清單（setupInputMethod、activateInputSource）。
func listInputMethod(string) {}
