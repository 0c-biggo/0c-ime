//go:build linux

package main

// 輸入法選單的更新狀態：讀 `0c-update status --json`，產生選單文字與要執行的命令。
// 文字與命令和 Fcitx5 外殼（menu.cpp）一致。

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type updateStatus struct {
	ok                                                                    bool
	installed, engine, mode, stage, target, blocked, detail, action       string
	pausedUntil, lastSuccess, lastError, selectionState, punctuationState string
	// vocab 是 features.vocab：0.2.0「我的詞彙」的入口（0.2.0 起恆為 true）。舊版 0c-update 沒有這個欄位，視為關閉。
	vocab bool
	// 「輸出簡體字」（#169）：settings.output_simplified 是設定檔的值（0.2.1 起的 0c-update 才有，沒有時 simplifiedKnown 為 false）；
	// runtime.output_simplified_live 時以 runtime.output_simplified（引擎目前的值）為準。
	simplifiedKnown, outputSimplified, simplifiedLive bool
}

func parseStatus(text string) updateStatus {
	var j struct {
		Installed   string `json:"installed"`
		Engine      string `json:"engine"`
		Mode        string `json:"mode"`
		Stage       string `json:"stage"`
		Action      string `json:"action"`
		PausedUntil string `json:"paused_until"`
		LastSuccess string `json:"last_success"`
		LastError   string `json:"last_error"`
		Target      *struct {
			Version string `json:"version"`
		} `json:"target"`
		Blocked *struct {
			Reason string `json:"reason"`
			Detail string `json:"detail"`
		} `json:"blocked"`
		Runtime *struct {
			SelectionState   string `json:"selection_state"`
			PunctuationState string `json:"punctuation_state"`
		} `json:"runtime"`
		Features json.RawMessage `json:"features"` // 另外解析：型別不對也不能讓整份狀態讀不出來
	}
	if json.Unmarshal([]byte(text), &j) != nil {
		return updateStatus{}
	}
	s := updateStatus{ok: true, installed: j.Installed, engine: j.Engine, mode: j.Mode, stage: j.Stage,
		action: j.Action, pausedUntil: j.PausedUntil, lastSuccess: j.LastSuccess, lastError: j.LastError}
	if s.mode == "" {
		s.mode = "auto"
	}
	if j.Target != nil {
		s.target = j.Target.Version
	}
	if j.Blocked != nil {
		s.blocked, s.detail = j.Blocked.Reason, j.Blocked.Detail
	}
	if j.Runtime != nil {
		s.selectionState, s.punctuationState = j.Runtime.SelectionState, j.Runtime.PunctuationState
	}
	var features map[string]any
	if json.Unmarshal(j.Features, &features) == nil {
		s.vocab, _ = features["vocab"].(bool)
	}
	// 「輸出簡體字」逐項另外解析，型別不對時只有這一項當成不知道。
	var loose struct{ Settings, Runtime map[string]any }
	if json.Unmarshal([]byte(text), &loose) == nil {
		if v, ok := loose.Settings["output_simplified"].(bool); ok {
			s.simplifiedKnown, s.outputSimplified = true, v
		}
		if live, _ := loose.Runtime["output_simplified_live"].(bool); live {
			if v, ok := loose.Runtime["output_simplified"].(bool); ok {
				s.simplifiedKnown, s.simplifiedLive, s.outputSimplified = true, true, v
			}
		}
	}
	return s
}

// simplifiedCommand 是舊引擎（不能即時切換）時「輸出簡體字」退回的命令：寫設定、交接重啟套用，0c-update 送桌面通知。
func simplifiedCommand(on bool) []string {
	value := "off"
	if on {
		value = "on"
	}
	return []string{"set", "output-simplified", value, "-apply", "-notify"}
}

func (s updateStatus) current() string {
	if s.engine != "" {
		return s.engine
	}
	return s.installed
}

// statusLabel 是更新列：目前版本或待辦。
func statusLabel(s updateStatus) string {
	if !s.ok {
		return "靈犀注音：無法讀取更新狀態"
	}
	if s.target != "" {
		switch {
		case s.blocked == "needs_consent":
			return "有新版本 " + s.target + " — 立即安裝"
		case s.blocked == "needs_relogin":
			if s.detail == "" {
				return s.target + "：登出再登入後生效"
			}
			return s.target + "：" + s.detail
		case s.blocked == "health_failed":
			return s.target + " 更新未完成，已保留原版本"
		case s.blocked == "waiting_idle":
			return s.target + " 已準備好，閒置時自動更新；可立即完成"
		case s.blocked == "engine_busy":
			return s.target + " 等待組字完成，再更新"
		case s.action == "finish":
			return s.target + " 已準備好 — 現在完成更新"
		case s.blocked == "metered_network":
			return s.target + " 等待非計量網路下載"
		case s.blocked == "low_battery":
			return s.target + " 等待充電後更新"
		}
		return s.target + " 更新中"
	}
	if len(s.pausedUntil) >= 10 {
		return "自動更新暫停至 " + s.pausedUntil[:10]
	} else if s.pausedUntil != "" {
		return "自動更新暫停至 " + s.pausedUntil
	}
	v := s.current()
	if v == "" {
		v = "（未知版本）"
	}
	state := "已是最新"
	if s.lastError != "" {
		state = "更新查詢失敗，稍後再試"
	} else if s.lastSuccess == "" {
		state = "尚未檢查更新"
	}
	return "靈犀注音 " + v + " · " + state
}

// statusCommand 是點更新列要執行的 0c-update 參數：有待辦時完成它，否則立即檢查。
func statusCommand(s updateStatus) []string {
	if s.action == "finish" {
		return []string{"finish", "-notify"}
	}
	return []string{"auto", "-now", "-notify"}
}

// updaterPath 是排程使用的穩定 0c-update；不存在時退回安裝器放在 ~/.local/bin 的版本。
func updaterPath() string {
	home := os.Getenv("HOME")
	for _, p := range []string{filepath.Join(home, ".local/share/zuin/bin/0c-update"), filepath.Join(home, ".local/bin/0c-update")} {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return filepath.Join(home, ".local/share/zuin/bin/0c-update")
}

// runUpdater 執行 0c-update 並取得輸出；逾時或失敗回傳 false。會阻塞，請在背景呼叫。
// 輸出寫到已刪除的暫存檔而不是 pipe：更新可能結束本行程，pipe 的另一端消失會讓更新器收到 SIGPIPE。
func runUpdater(args []string, timeout time.Duration) (string, bool) {
	f, err := os.CreateTemp("", "0c-ibus-*")
	if err != nil {
		return "", false
	}
	os.Remove(f.Name())
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, updaterPath(), args...)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	runErr := cmd.Run()
	f.Seek(0, io.SeekStart)
	out, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(out), runErr == nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
