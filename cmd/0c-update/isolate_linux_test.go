package main

import (
	"context"
	"os"
	"os/exec"
)

func isolateResidentEngine() func() {
	runtimeDir, err := os.MkdirTemp("", "0c-update-test-runtime-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_RUNTIME_DIR", runtimeDir) // dialEngine 連不到真的引擎，也就不會交接它
	runSystemdRun = func(context.Context, string, []string) ([]byte, error) { return nil, nil }
	spawnEngineDirect = func(string, ...string) error { return nil }
	// 不讀寫開發機真的 kwinrc，交還 KWin 的紀錄寫在暫存目錄（#204）。
	kconfigTool = func(...string) (string, error) { return "", exec.ErrNotFound }
	stateDir, err := os.MkdirTemp("", "0c-update-test-kwin-")
	if err != nil {
		panic(err)
	}
	kwinStateDir = func() string { return stateDir }
	return func() { os.RemoveAll(runtimeDir); os.RemoveAll(stateDir) }
}
