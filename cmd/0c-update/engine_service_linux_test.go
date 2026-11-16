package main

import (
	"context"
	"os"
	"testing"
	"time"
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
	spawnEngineDirect = func(string, ...string) error { t.Fatal("systemd-run 成功時不直接啟動"); return nil }
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
	saved, savedAlive := engineRunning, engineProcessAlive
	t.Cleanup(func() { engineRunning, engineProcessAlive = saved, savedAlive })
	asked := 0
	engineRunning = func() bool { asked++; return false }
	engineProcessAlive = func() bool { return false }
	t.Setenv("HOME", t.TempDir())
	writeSettings(t, func() string { os.MkdirAll(basePath(), 0o700); return basePath() }(), `{"preview_0_2":false}`)
	drainEngineForInstaller()
	if asked != 1 {
		t.Fatalf("不看設定檔，直接檢查引擎有沒有在跑：%d", asked)
	}
}

// #280：裝好馬上解除安裝時，引擎行程已經在、還沒開始接受連線。等它就緒再交接，不當成沒在跑；
// 沒有引擎行程時不等；行程一直不接受連線時等到上限就放棄，交給安裝器照舊說明。
func TestEngineAcceptingWaitsForStartingEngine(t *testing.T) {
	saved, savedAlive, savedWait := engineRunning, engineProcessAlive, engineStartWait
	t.Cleanup(func() { engineRunning, engineProcessAlive, engineStartWait = saved, savedAlive, savedWait })
	dials, alive := 0, true
	engineProcessAlive = func() bool { return alive }

	engineRunning = func() bool { dials++; return dials >= 3 }
	if !engineAccepting() || dials != 3 {
		t.Fatalf("剛啟動的引擎：等到可以連線：%d", dials)
	}

	dials, alive = 0, false
	engineRunning = func() bool { dials++; return false }
	if engineAccepting() || dials != 1 {
		t.Fatalf("沒有引擎行程：不等：%d", dials)
	}

	dials, alive, engineStartWait = 0, true, 250*time.Millisecond
	start := time.Now()
	if engineAccepting() {
		t.Fatal("一直不接受連線的引擎：不算可交接")
	}
	if took := time.Since(start); took < engineStartWait || took > 2*time.Second {
		t.Fatalf("等到上限就放棄：%v", took)
	}
}
