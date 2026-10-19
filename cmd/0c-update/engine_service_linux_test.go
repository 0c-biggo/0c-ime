package main

import (
	"context"
	"os"
	"testing"
)

// 0.2.0 起引擎常駐對所有人生效（#120）：引擎沒在跑時每次喚起都叫起，舊設定檔寫著 preview_0_2=false 也一樣；
// 引擎已在跑時不動。
func TestEnsureEngineServiceStartsResidentEngine(t *testing.T) {
	base := t.TempDir()
	savedRunning, savedLook, savedRun, savedDirect := engineRunning, lookSystemdRun, runSystemdRun, spawnEngineDirect
	t.Cleanup(func() {
		engineRunning, lookSystemdRun, runSystemdRun, spawnEngineDirect = savedRunning, savedLook, savedRun, savedDirect
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(home+"/.local/bin", 0o755)
	os.WriteFile(home+"/.local/bin/0c-server", []byte("#!/bin/sh\n"), 0o755)
	starts := 0
	lookSystemdRun = func() (string, error) { return "/usr/bin/systemd-run", nil }
	runSystemdRun = func(_ context.Context, _ string, args []string) ([]byte, error) { starts++; return nil, nil }
	spawnEngineDirect = func(string) error { t.Fatal("systemd-run 成功時不直接啟動"); return nil }
	running := false
	engineRunning = func() bool { return running }

	ensureEngineService(base)
	if starts != 1 {
		t.Fatalf("沒有設定檔、引擎沒在跑：啟動一次：%d", starts)
	}
	writeSettings(t, base, `{"preview_0_2":false}`)
	ensureEngineService(base)
	if starts != 2 {
		t.Fatalf("舊設定檔的 preview_0_2=false 不再擋住常駐：%d", starts)
	}
	running = true
	ensureEngineService(base)
	if starts != 2 {
		t.Fatal("引擎已在跑時不再啟動")
	}
	if state, sub := engineServiceState(); state != "timer" || sub != "running" {
		t.Fatalf("%s %s", state, sub)
	}
}

// 安裝、回復、移除前：外殼都沒在執行而引擎還在時，一律請它交接退出（不再看 preview_0_2）；引擎沒在跑時不動。
func TestDrainEngineForInstallerWithoutSwitch(t *testing.T) {
	saved := engineRunning
	t.Cleanup(func() { engineRunning = saved })
	asked := 0
	engineRunning = func() bool { asked++; return false }
	t.Setenv("HOME", t.TempDir())
	writeSettings(t, func() string { os.MkdirAll(basePath(), 0o700); return basePath() }(), `{"preview_0_2":false}`)
	drainEngineForInstaller()
	if asked != 1 {
		t.Fatalf("不看設定檔，直接檢查引擎有沒有在跑：%d", asked)
	}
}
