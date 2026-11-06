package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// macOS 輸入法外殼的診斷計數（#266）：外殼寫在個人資料目錄的 input-method-stats.json（platforms/macos/Sources/ShellStats.swift），
// 匯出狀態時附在 shell.input_method_stats。只有次數、時間與浮動組字（#84）用在哪些 App 的 bundle ID，沒有按鍵或文字內容。
// 只照抄認得的欄位，檔案太大、壞掉或不存在時不附。
const (
	inputMethodStatsFile  = "input-method-stats.json"
	inputMethodStatsLimit = 64 << 10
	inputMethodStatsApps  = 20
)

func inputMethodStats(base string) map[string]any {
	f, err := os.Open(filepath.Join(base, inputMethodStatsFile))
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, inputMethodStatsLimit+1))
	if err != nil || len(data) > inputMethodStatsLimit {
		return nil
	}
	var raw struct {
		Schema               int    `json:"schema"`
		PID                  int    `json:"pid"`
		Version              string `json:"version"`
		StartedAt            string `json:"started_at"`
		WrittenAt            string `json:"written_at"`
		Activations          int    `json:"activations"`
		Deactivations        int    `json:"deactivations"`
		Keys                 int    `json:"keys"`
		ActivationsSinceWake int    `json:"activations_since_wake"`
		KeysSinceWake        int    `json:"keys_since_wake"`
		LastActivateAt       string `json:"last_activate_at"`
		LastKeyAt            string `json:"last_key_at"`
		LastWakeAt           string `json:"last_wake_at"`
		LastSleepAt          string `json:"last_sleep_at"`
		Floating             struct {
			Rescues int            `json:"rescues"`
			Fields  int            `json:"fields"`
			Apps    map[string]int `json:"apps"`
		} `json:"floating"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Schema != 1 {
		return nil
	}
	out := map[string]any{
		"pid": raw.PID, "started_at": raw.StartedAt, "written_at": raw.WrittenAt,
		"activations": raw.Activations, "deactivations": raw.Deactivations, "keys": raw.Keys,
		"activations_since_wake": raw.ActivationsSinceWake, "keys_since_wake": raw.KeysSinceWake,
	}
	for key, value := range map[string]string{"version": raw.Version, "last_activate_at": raw.LastActivateAt,
		"last_key_at": raw.LastKeyAt, "last_wake_at": raw.LastWakeAt, "last_sleep_at": raw.LastSleepAt} {
		if value != "" {
			out[key] = value
		}
	}
	apps := map[string]int{}
	for id, n := range raw.Floating.Apps {
		if len(apps) == inputMethodStatsApps {
			break
		}
		if len(id) <= 128 {
			apps[id] = n
		}
	}
	out["floating"] = map[string]any{"rescues": raw.Floating.Rescues, "fields": raw.Floating.Fields, "apps": apps}
	return out
}
