package main

import (
	"errors"
	"testing"
)

// 0.2.0 起引擎常駐對所有人生效（#120）：更新 agent 每次喚起、引擎沒在跑就啟動，舊設定檔寫著 preview_0_2=false 也一樣。
func TestEnsureEngineServiceIgnoresRetiredSwitch(t *testing.T) {
	saved := spawnResident
	t.Cleanup(func() { spawnResident = saved })
	starts := 0
	spawnResident = func() error { starts++; return nil }
	t.Setenv("HOME", t.TempDir()) // 引擎 socket 不存在
	base := t.TempDir()
	writeSettings(t, base, `{"preview_0_2":false}`)
	ensureEngineService(base)
	if starts != 1 {
		t.Fatalf("引擎沒在跑：啟動一次：%d", starts)
	}
}

// pkg 的 postinstall 衍生的引擎會被 syspolicyd 終止（腳本目錄已刪）：安裝後改請更新 agent 啟動引擎；
// agent 自己安裝（自動更新）或 kickstart 失敗（沒註冊、被關閉）時才直接啟動。
func TestEngineAfterInstallAsksAgent(t *testing.T) {
	savedSpawn, savedKick := spawnResident, kickstartAgent
	t.Cleanup(func() { spawnResident, kickstartAgent = savedSpawn, savedKick })
	t.Setenv("HOME", t.TempDir()) // 引擎 socket 不存在
	for _, c := range []struct {
		name, service         string
		kickErr               error
		wantKicks, wantSpawns int
	}{
		{"安裝程式或終端機", "", nil, 1, 0},
		{"agent 沒註冊或被關閉", "", errors.New("Could not find service"), 1, 1},
		{"自動更新由 agent 安裝", agentLabel, nil, 0, 1},
	} {
		kicks, spawns := 0, 0
		kickstartAgent = func() error { kicks++; return c.kickErr }
		spawnResident = func() error { spawns++; return nil }
		t.Setenv("XPC_SERVICE_NAME", c.service)
		engineAfterInstall(t.TempDir())
		if kicks != c.wantKicks || spawns != c.wantSpawns {
			t.Errorf("%s：kickstart %d 次、直接啟動 %d 次，要 %d、%d", c.name, kicks, spawns, c.wantKicks, c.wantSpawns)
		}
	}
}
