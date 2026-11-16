// Package updatew 是 Windows 背景更新啟動器 0c-updatew 的固定契約與「退回上一版更新器」的判斷。
//
// 契約（永久不變；改了舊排程、舊協定就叫不起來，見 docs/updating.md〈啟動器的固定契約〉）：
//   - 啟動器在 <個人資料目錄>\bin\0c-updatew.exe，排程與 zc-update: 協定只以三種方式執行它：
//     `0c-updatew.exe auto`、`0c-updatew.exe engine`、`0c-updatew.exe protocol zc-update:…`。
//   - 它把同樣的參數交給同一個目錄的 0c-update.exe；那一份連續異常時改交給 0c-update.prev.exe（上一版）。
//   - 0c-update 對這三種呼叫的結束碼：0 正常；75（ExitTemporary）是預期中的暫時失敗（網路、另一個更新正在執行、
//     使用者取消 UAC），不算更新器異常；其他結束碼、當掉（NTSTATUS 例外碼、Go runtime 的 2）與啟動失敗都算異常。
//     protocol 由使用者按通知按鈕觸發，一般的錯誤結束（例如待辦版本已變更）不算異常，只有當掉與啟動失敗算。
//   - 紀錄在 <個人資料目錄>\update\updatew.json（這個套件的 State）；每次的輸出在 update\<動作>-last.log。
//
// 為什麼：穩定副本 bin\0c-update.exe 是排程唯一會執行的更新器，新版有 bug（啟動就當掉、讀不了狀態、
// 驗不過索引簽章）時，背景更新就永遠停在這一版，連修正版都裝不上。Chromium updater 讓新版先 qualify 才接手、
// 舊版並存；Omaha 的 GoogleUpdate.exe 是不常改的殼，實際做事的是版本目錄裡的程式。這裡取同樣的分工：
// 啟動器不含更新邏輯、幾乎不改，負責在新版異常時改用上一版，讓上一版把下一個修正版裝上來。
package updatew

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
)

const (
	// ExitTemporary 是 0c-update 以啟動器的三種呼叫執行、遇到預期中的暫時失敗時的結束碼（sysexits 的 EX_TEMPFAIL）。
	ExitTemporary = 75
	// FailureLimit：同一份 0c-update 連續這麼多次異常就改用上一版；上一版也連續這麼多次異常就改回來。
	FailureLimit = 3

	UpdaterName  = "0c-update.exe"
	PreviousName = "0c-update.prev.exe"
	LauncherName = "0c-updatew.exe"

	stateSchema = 1
	maxEvents   = 20
	// 查詢一直失敗多久之後，試一次上一版能不能查到（排除「新版自己的連線或驗章有 bug，卻都回報成網路失敗」）。
	probeAfter = 72 * time.Hour
	probeEvery = 24 * time.Hour
)

// Verb 回報 v 是不是啟動器會執行的三種呼叫之一。
func Verb(v string) bool { return v == "auto" || v == "engine" || v == "protocol" }

// Outcome 是一次執行的分類。
type Outcome string

const (
	OK        Outcome = "ok"
	Temporary Outcome = "temporary"
	Abnormal  Outcome = "abnormal"
)

// Classify 依契約分類一次執行：startErr 不是 nil 表示沒啟動起來（檔案不見、被防毒隔離、映像損毀）。
func Classify(verb string, startErr error, code int) (Outcome, string) {
	switch {
	case startErr != nil:
		return Abnormal, "無法啟動：" + startErr.Error()
	case code == 0:
		return OK, ""
	case code == ExitTemporary:
		return Temporary, "暫時失敗（結束碼 75）"
	case uint32(code) >= 0xC0000000:
		return Abnormal, fmt.Sprintf("當掉（0x%08X）", uint32(code))
	case code == 2:
		return Abnormal, "當掉（結束碼 2）" // Go runtime 的 panic 與 fatal error
	case verb == "protocol":
		return Temporary, fmt.Sprintf("通知按鈕的操作沒有完成（結束碼 %d）", code)
	}
	return Abnormal, fmt.Sprintf("錯誤結束（結束碼 %d）", code)
}

// State 是 update\updatew.json。計數只對應一份穩定副本（以 SHA-256 識別）：換上不同內容的 0c-update 就重新計算。
type State struct {
	Schema int `json:"schema"`
	// Stable 是計數所屬的 bin\0c-update.exe 的 SHA-256。
	Stable string `json:"stable_sha256,omitempty"`
	// Failures 是 Stable 連續異常的次數；正常結束就歸零，暫時失敗不增不減。
	Failures int `json:"failures,omitempty"`
	// Fallback 不是 nil 表示 Stable 判定有問題，改執行上一版，直到換上不同內容的 0c-update。
	Fallback *Fallback `json:"fallback,omitempty"`
	// PrevFailures 是改用上一版之後，上一版連續異常的次數。
	PrevFailures int `json:"prev_failures,omitempty"`
	// Retry：退回之後換上了新的 0c-update，正在試它；第一次正常結束時記一筆 recovered。
	Retry  bool      `json:"retry,omitempty"`
	LastOK time.Time `json:"last_ok,omitzero"`
	// Probe 是最近一次以上一版測試查詢的時間（見 probeDue）。
	Probe  time.Time `json:"probe,omitzero"`
	Last   *Run      `json:"last,omitempty"`
	Events []Event   `json:"events,omitempty"`
}

type Fallback struct {
	Since  time.Time `json:"since"`
	Reason string    `json:"reason"`
	Prev   string    `json:"prev_sha256,omitempty"`
}

type Run struct {
	At     time.Time `json:"at"`
	Verb   string    `json:"verb"`
	Target string    `json:"target"` // stable 或 prev
	Result Outcome   `json:"result"`
	Detail string    `json:"detail,omitempty"`
}

type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

// StatePath 是紀錄檔位置。
func StatePath(base string) string { return filepath.Join(base, "update", "updatew.json") }

var errNewerState = errors.New("updatew.json 是較新的格式")

// Load 讀紀錄；不存在時回傳空的紀錄。較新格式或損毀時回傳錯誤，呼叫端不寫回、照常執行穩定副本。
func Load(base string) (*State, error) {
	b, err := os.ReadFile(StatePath(base))
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Schema: stateSchema}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Schema > stateSchema {
		return nil, errNewerState
	}
	s.Schema = stateSchema
	return &s, nil
}

// save 只在 0c-update 已建立的 update\ 目錄裡寫（不自己建目錄：解除安裝後殘留的排程不在使用者目錄留下東西）。
func (s *State) save(base string) error {
	if info, err := os.Stat(filepath.Dir(StatePath(base))); err != nil || !info.IsDir() {
		return nil
	}
	return atomicfile.WriteJSON(StatePath(base), s)
}

// Failing 回報內容為 hash 的穩定副本是不是已判定有問題（啟動器正改用上一版）。換穩定副本時，這樣的檔案不保留為
// 上一版，保留原本那份可用的上一版。
func Failing(base, hash string) bool {
	s, err := Load(base)
	return err == nil && hash != "" && s.Fallback != nil && s.Stable == hash
}

func (s *State) event(now time.Time, kind, detail string) {
	s.Events = append(s.Events, Event{At: now, Kind: kind, Detail: detail})
	if len(s.Events) > maxEvents {
		s.Events = append([]Event(nil), s.Events[len(s.Events)-maxEvents:]...)
	}
}

// files 是這次執行時兩份更新器的 SHA-256；讀不到（不存在、被隔離、被鎖）時為空字串。
type files struct{ stable, prev string }

// prevUsable：有上一版，而且內容跟穩定副本不同（相同就沒有退回的意義）。
func (f files) prevUsable() bool { return f.prev != "" && f.prev != f.stable }

// observe：穩定副本換成不同內容（新版裝上來了、或使用者重裝）時重新計數；退回中就記一筆、改試新的。
// 讀不到穩定副本時（被防毒隔離）沿用原本的計數，補回同一個檔案時仍維持退回。
func (s *State) observe(f files, now time.Time) {
	if f.stable == "" || f.stable == s.Stable {
		return
	}
	if s.Fallback != nil {
		s.event(now, "new_stable", "換上了新的 0c-update，改回執行它")
		s.Retry = true
	}
	s.Stable, s.Failures, s.Fallback, s.PrevFailures, s.Probe = f.stable, 0, nil, 0, time.Time{}
}

// UpdateView 是 update\state.json 裡判斷「查詢是不是一直失敗」要用的欄位（autoupdate.State 的同名欄位）。
type UpdateView struct {
	Mode          string    `json:"mode"`
	PausedUntil   time.Time `json:"paused_until"`
	LastCheck     time.Time `json:"last_check"`
	LastSuccess   time.Time `json:"last_success"`
	NextCheck     time.Time `json:"next_check"`
	CheckFailures int       `json:"check_failures"`
}

// ReadUpdateView 讀 state.json；讀不到或格式不對時回傳 nil（不做查詢測試）。
func ReadUpdateView(base string) *UpdateView {
	b, err := os.ReadFile(filepath.Join(base, "update", "state.json"))
	if err != nil {
		return nil
	}
	var v UpdateView
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return &v
}

// probeDue：auto 的查詢失敗結果記在 state.json、結束碼仍是 0（網路斷線不是錯誤），所以「新版自己連不上、
// 驗不過索引」從結束碼看不出來。查詢已經連續失敗超過 probeAfter（曾經成功過、這次輪到查詢、沒有暫停或手動模式），
// 就每 probeEvery 以上一版執行一次：上一版查得到，表示問題在新版。
func (s *State) probeDue(verb string, v *UpdateView, now time.Time) bool {
	return verb == "auto" && v != nil && v.Mode != "manual" && !now.Before(v.PausedUntil) &&
		v.CheckFailures >= FailureLimit && !v.LastSuccess.IsZero() && v.LastCheck.Sub(v.LastSuccess) >= probeAfter &&
		!now.Before(v.NextCheck) && (s.Probe.IsZero() || now.Sub(s.Probe) >= probeEvery)
}

const (
	targetStable = "stable"
	targetPrev   = "prev"
)

// choose 決定這次執行哪一份；probe 表示這次以上一版做查詢測試。
func (s *State) choose(f files, verb string, v *UpdateView, now time.Time) (target string, probe bool) {
	if !f.prevUsable() {
		return targetStable, false
	}
	if s.Fallback != nil {
		return targetPrev, false
	}
	if s.probeDue(verb, v, now) {
		s.Probe = now
		return targetPrev, true
	}
	return targetStable, false
}

// record 記下一次執行的結果；回傳要不要馬上改執行另一份（這次選的那份根本沒啟動起來，另一份可用）。
func (s *State) record(f files, verb, target string, probe bool, outcome Outcome, detail string, started bool,
	before, after *UpdateView, now time.Time) (runOther bool) {
	s.Last = &Run{At: now, Verb: verb, Target: target, Result: outcome, Detail: detail}
	if target == targetStable {
		switch outcome {
		case OK:
			s.Failures, s.LastOK = 0, now
			if s.Retry {
				s.Retry = false
				s.event(now, "recovered", "新的 0c-update 正常完成，清除退回紀錄")
			}
		case Abnormal:
			s.Failures++
			if s.Fallback == nil && s.Failures >= FailureLimit && f.prevUsable() {
				s.Fallback = &Fallback{Since: now, Reason: fmt.Sprintf("0c-update 連續 %d 次異常，最後一次：%s", s.Failures, detail), Prev: f.prev}
				s.PrevFailures, s.Retry = 0, false
				s.event(now, "fallback", s.Fallback.Reason)
			}
		}
		return !started && f.prevUsable()
	}
	if probe {
		if outcome == OK && before != nil && after != nil && after.LastSuccess.After(before.LastSuccess) {
			s.Fallback = &Fallback{Since: now, Reason: fmt.Sprintf("0c-update 自 %s 起查詢更新一直失敗，上一版查得到", before.LastSuccess.Format(time.RFC3339)), Prev: f.prev}
			s.PrevFailures, s.Retry = 0, false
			s.event(now, "fallback", s.Fallback.Reason)
		} else {
			s.event(now, "probe", "上一版同樣查詢失敗或沒有查詢，視為網路或伺服器問題："+string(outcome)+" "+detail)
		}
		return false
	}
	switch outcome {
	case OK:
		s.PrevFailures = 0
	case Abnormal:
		s.PrevFailures++
		if s.Fallback != nil && s.PrevFailures >= FailureLimit {
			s.event(now, "fallback_ended", fmt.Sprintf("上一版也連續 %d 次異常，改回執行目前的 0c-update：%s", s.PrevFailures, detail))
			s.Fallback, s.Failures, s.PrevFailures = nil, 0, 0
		}
	}
	return !started && f.stable != ""
}
