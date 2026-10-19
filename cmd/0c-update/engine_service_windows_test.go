package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 0.2.0 起引擎常駐對所有人生效（#120）：自動更新每次喚起、已安裝而引擎沒在跑就請登入工作啟動，
// 舊設定檔寫著 preview_0_2=false 也一樣。
func TestEnsureEngineServiceIgnoresRetiredSwitch(t *testing.T) {
	if c, err := dialEngine(time.Second); err == nil {
		c.Close()
		t.Skip("這台機器的引擎正在執行：ensureEngineService 不會啟動")
	}
	saved := startResident
	t.Cleanup(func() { startResident = saved })
	starts := 0
	startResident = func(string) error { starts++; return nil }
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := os.MkdirAll(basePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, basePath(), `{"preview_0_2":false}`)
	ensureEngineService(basePath())
	if starts != 0 {
		t.Fatal("沒有安裝時不啟動")
	}
	if err := os.WriteFile(filepath.Join(basePath(), "install-receipt.json"), []byte(`{"version":"0.2.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ensureEngineService(basePath())
	if starts != 1 {
		t.Fatalf("已安裝、引擎沒在跑：啟動一次：%d", starts)
	}
}

// 安裝、回復換了引擎指標後（#120）：常駐的是別的版本才請它交接，交接成功才叫起新引擎；仍在組字就不換。
func TestHandOverResidentEngine(t *testing.T) {
	type call struct{ drains, starts int }
	run := func(version string, info map[string]string, running bool, drainErr error) call {
		var c call
		handOverResidentEngine(version, func() (map[string]string, bool) { return info, running },
			func() error { c.drains++; return drainErr }, func() { c.starts++ })
		return c
	}
	if c := run("0.2.0", nil, false, nil); c != (call{}) {
		t.Fatalf("引擎沒在跑：不交接也不啟動（交給 afterInstall／下一個按鍵）：%+v", c)
	}
	if c := run("0.2.0", map[string]string{"version": "0.2.0"}, true, nil); c != (call{}) {
		t.Fatalf("已經是新版（重裝同一版）：不打斷：%+v", c)
	}
	if c := run("0.2.0", map[string]string{"version": "0.1.0-dev.64"}, true, nil); c != (call{1, 1}) {
		t.Fatalf("舊版常駐：交接後叫起新版：%+v", c)
	}
	if c := run("0.2.0", map[string]string{"version": "0.1.0-dev.64"}, true, errors.New("仍在組字")); c != (call{1, 0}) {
		t.Fatalf("仍在組字：保留舊引擎、不另外啟動：%+v", c)
	}
	if c := run("", map[string]string{"version": "0.1.0-dev.64"}, true, nil); c != (call{}) {
		t.Fatalf("讀不到安裝版本：不動引擎：%+v", c)
	}
	// start 為 nil（install：新引擎由 afterInstall 經登入工作叫起）時只交接。
	drained := false
	handOverResidentEngine("0.2.0", func() (map[string]string, bool) { return map[string]string{"version": "0.1.0"}, true },
		func() error { drained = true; return nil }, nil)
	if !drained {
		t.Fatal("start 為 nil 仍要交接舊引擎")
	}
}
