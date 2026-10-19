//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"git.biggo.com/0c/ime/internal/enginedir"
)

// redirectEngine：外殼以預設方式啟動引擎時，若 engine.json 指向另一個版本目錄，
// 改由該版引擎（與其配對字典）執行。明確指定 -data 的啟動（測試、開發）不轉交。
func redirectEngine(base string) {
	if os.Getenv("ZC_ENGINE_REDIRECTED") != "" {
		return
	}
	for _, a := range os.Args[1:] {
		if a == "-data" || a == "--data" || strings.HasPrefix(a, "-data=") || strings.HasPrefix(a, "--data=") {
			return
		}
	}
	exe, data, ok := enginedir.Resolve(base)
	if !ok {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if a, err1 := filepath.EvalSymlinks(self); err1 == nil {
		if b, err2 := filepath.EvalSymlinks(exe); err2 == nil && a == b {
			return
		}
	}
	os.Setenv("ZC_ENGINE_REDIRECTED", "1")
	args := append([]string{exe, "-data", data}, os.Args[1:]...)
	_ = syscall.Exec(exe, args, os.Environ()) // 失敗時沿用本程式
}
