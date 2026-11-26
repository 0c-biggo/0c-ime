package updatew

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 測試用的「更新器」：檔案內容是它的行為，例如 "v2 exit=0xC0000005"。檔案不存在就是啟動失敗（被隔離）。
type fakeHost struct {
	t    *testing.T
	base string
	now  time.Time
	ran  []string // 執行過的檔名
	view *UpdateView
	// afterRun 讓測試模擬子行程改寫 state.json（例如上一版查詢成功）。
	afterRun func(name string)
}

func newHost(t *testing.T) *fakeHost {
	base := filepath.Join(t.TempDir(), "Zuin")
	for _, d := range []string{"bin", "update"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeHost{t: t, base: base, now: time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)}
}

func (h *fakeHost) put(name, behaviour string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.base, "bin", name), []byte(behaviour), 0o700); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHost) remove(name string) { os.Remove(filepath.Join(h.base, "bin", name)) }

func (h *fakeHost) env() Env {
	return Env{
		Base: h.base,
		Now:  func() time.Time { return h.now },
		Hash: func(path string) string {
			b, err := os.ReadFile(path)
			if err != nil {
				return ""
			}
			sum := sha256.Sum256(b)
			return hex.EncodeToString(sum[:])
		},
		Exec: func(path string, args []string) (error, int) {
			b, err := os.ReadFile(path)
			if err != nil {
				return err, 0
			}
			h.ran = append(h.ran, filepath.Base(path))
			if h.afterRun != nil {
				h.afterRun(filepath.Base(path))
			}
			_, code, _ := strings.Cut(string(b), "exit=")
			n, err := strconv.ParseInt(code, 0, 64)
			if err != nil {
				h.t.Fatalf("bad behaviour %q", b)
			}
			return nil, int(n)
		},
		View: func() *UpdateView { return h.view },
	}
}

// run 執行一次啟動器（每次相隔 15 分鐘，跟排程一樣），回傳結束碼與這次執行了哪些檔案。
func (h *fakeHost) run(args ...string) (int, []string) {
	h.now = h.now.Add(15 * time.Minute)
	h.ran = nil
	code := Launch(h.env(), args)
	return code, h.ran
}

func (h *fakeHost) state() *State {
	h.t.Helper()
	s, err := Load(h.base)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func hasEvent(s *State, kind string) bool {
	for _, e := range s.Events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		verb  string
		start error
		code  int
		want  Outcome
	}{
		{"auto", nil, 0, OK},
		{"auto", nil, ExitTemporary, Temporary},
		{"auto", nil, 1, Abnormal},
		{"engine", nil, 1, Abnormal},
		{"auto", nil, 2, Abnormal},
		{"auto", nil, int(uint32(0xC0000005)), Abnormal}, // access violation
		{"auto", nil, int(uint32(0xC0000135)), Abnormal}, // DLL not found
		{"auto", errors.New("file not found"), 0, Abnormal},
		{"protocol", nil, 1, Temporary}, // 使用者按通知按鈕的結果（取消 UAC、版本已變更）
		{"protocol", nil, 2, Abnormal},
		{"protocol", nil, int(uint32(0xC0000409)), Abnormal},
		{"protocol", errors.New("access denied"), 0, Abnormal},
	} {
		if got, _ := Classify(c.verb, c.start, c.code); got != c.want {
			t.Errorf("Classify(%s, %v, %#x) = %s, want %s", c.verb, c.start, c.code, got, c.want)
		}
	}
}

// 新版連續 3 次當掉：第 4 次起執行上一版，並記錄原因。
func TestFallsBackToPreviousAfterConsecutiveCrashes(t *testing.T) {
	h := newHost(t)
	h.put(UpdaterName, "v3 exit=0xC0000005")
	h.put(PreviousName, "v2 exit=0")
	for i := 1; i <= FailureLimit; i++ {
		code, ran := h.run("auto")
		if len(ran) != 1 || ran[0] != UpdaterName || uint32(code) != 0xC0000005 {
			t.Fatalf("run %d: code %#x ran %v", i, code, ran)
		}
	}
	s := h.state()
	if s.Fallback == nil || s.Failures != FailureLimit || !hasEvent(s, "fallback") || !strings.Contains(s.Fallback.Reason, "0xC0000005") {
		t.Fatalf("state after %d crashes: %+v", FailureLimit, s)
	}
	for _, verb := range [][]string{{"auto"}, {"engine"}, {"protocol", "zc-update:finish?version=0.2.13"}} {
		code, ran := h.run(verb...)
		if code != 0 || len(ran) != 1 || ran[0] != PreviousName {
			t.Fatalf("%v during fallback: code %d ran %v", verb, code, ran)
		}
	}
	if last := h.state().Last; last.Target != targetPrev || last.Result != OK {
		t.Fatalf("last run %+v", last)
	}
}

// 非網路類的錯誤結束（結束碼 1）同樣算異常；中間有一次正常結束就重新計算。
func TestSuccessResetsTheCount(t *testing.T) {
	h := newHost(t)
	h.put(PreviousName, "v2 exit=0")
	h.put(UpdaterName, "v3 exit=1")
	h.run("auto")
	h.run("engine")
	s := h.state()
	if s.Failures != 2 {
		t.Fatalf("failures %d", s.Failures)
	}
	// 同一份檔案這次正常結束（例如 engine 失敗是引擎的事，auto 照常）：歸零。
	env := h.env()
	exec := env.Exec
	env.Exec = func(path string, args []string) (error, int) {
		if args[0] == "auto" {
			h.ran = append(h.ran, filepath.Base(path))
			return nil, 0
		}
		return exec(path, args)
	}
	h.now = h.now.Add(15 * time.Minute)
	Launch(env, []string{"auto"})
	if s := h.state(); s.Failures != 0 || s.LastOK.IsZero() {
		t.Fatalf("success did not reset: %+v", s)
	}
	h.run("engine")
	h.run("engine")
	if s := h.state(); s.Fallback != nil || s.Failures != 2 {
		t.Fatalf("2 failures after a success must not fall back: %+v", s)
	}
}

// 網路斷線：auto 照常結束（0，查詢失敗記在 state.json），或回報暫時失敗（75，例如另一個更新正在執行）——都不退回。
func TestTemporaryFailuresDoNotFallBack(t *testing.T) {
	h := newHost(t)
	h.put(PreviousName, "v2 exit=0")
	h.put(UpdaterName, "v3 exit=75")
	for i := 0; i < 10; i++ {
		if code, ran := h.run("auto"); code != ExitTemporary || ran[0] != UpdaterName {
			t.Fatalf("run %d: %d %v", i, code, ran)
		}
	}
	if s := h.state(); s.Fallback != nil || s.Failures != 0 {
		t.Fatalf("temporary failures counted: %+v", s)
	}
	// 通知按鈕的一般錯誤結束（使用者取消 UAC、待辦版本已變更）也不算。
	h.put(UpdaterName, "v3 exit=1")
	for i := 0; i < 5; i++ {
		h.run("protocol", "zc-update:finish")
	}
	if s := h.state(); s.Fallback != nil || s.Failures != 0 {
		t.Fatalf("protocol errors counted: %+v", s)
	}
}

// 退回之後換上了新的 0c-update（上一版把修正版裝上來）：改試新的，成功一次就清除。
func TestNewUpdaterIsTriedAgainAndRecovers(t *testing.T) {
	h := newHost(t)
	h.put(UpdaterName, "v3 exit=2")
	h.put(PreviousName, "v2 exit=0")
	for i := 0; i < FailureLimit; i++ {
		h.run("auto")
	}
	if h.state().Fallback == nil {
		t.Fatal("no fallback")
	}
	h.put(UpdaterName, "v4 exit=0")
	if code, ran := h.run("auto"); code != 0 || len(ran) != 1 || ran[0] != UpdaterName {
		t.Fatalf("new updater not tried: %d %v", code, ran)
	}
	s := h.state()
	if s.Fallback != nil || s.Failures != 0 || s.Retry || !hasEvent(s, "new_stable") || !hasEvent(s, "recovered") {
		t.Fatalf("not recovered: %+v", s)
	}
}

// 防毒隔離同一個雜湊時，修復程序補回的是同一個檔案：仍維持退回，不再反覆執行會被隔離的那一份。
func TestQuarantinedUpdaterRunsPreviousRightAway(t *testing.T) {
	h := newHost(t)
	h.put(UpdaterName, "v3 exit=0")
	h.put(PreviousName, "v2 exit=0")
	h.run("auto")
	h.remove(UpdaterName) // 被隔離
	for i := 1; i <= FailureLimit; i++ {
		code, ran := h.run("auto")
		// 這一次就改執行上一版：穩定副本根本沒有啟動，沒有重複執行的疑慮。
		if code != 0 || len(ran) != 1 || ran[0] != PreviousName {
			t.Fatalf("run %d: %d %v", i, code, ran)
		}
	}
	s := h.state()
	if s.Fallback == nil {
		t.Fatalf("no fallback after %d start failures: %+v", FailureLimit, s)
	}
	h.put(UpdaterName, "v3 exit=0") // 修復補回同一個檔案
	if _, ran := h.run("auto"); ran[0] != PreviousName {
		t.Fatalf("restored copy of the quarantined file ran again: %v", ran)
	}
}

// 沒有上一版（第一次裝、從 0.2.11 升上來還沒換過穩定副本），或上一版跟目前同一份：照舊執行目前的，只記數。
func TestWithoutPreviousKeepsRunningStable(t *testing.T) {
	for _, prev := range []string{"", "v3 exit=0xC0000005"} {
		h := newHost(t)
		h.put(UpdaterName, "v3 exit=0xC0000005")
		if prev != "" {
			h.put(PreviousName, prev)
		}
		for i := 0; i < FailureLimit+2; i++ {
			if _, ran := h.run("auto"); len(ran) != 1 || ran[0] != UpdaterName {
				t.Fatalf("prev %q run %d: %v", prev, i, ran)
			}
		}
		if s := h.state(); s.Fallback != nil || s.Failures != FailureLimit+2 {
			t.Fatalf("prev %q: %+v", prev, s)
		}
		// 兩份都沒有：沒東西可以執行，回傳 1，不當掉。
		h.remove(UpdaterName)
		h.remove(PreviousName)
		if code, ran := h.run("auto"); code != 1 || len(ran) != 0 {
			t.Fatalf("prev %q: missing both: code %d ran %v", prev, code, ran)
		}
	}
}

// 上一版也連續異常（例如問題其實在環境）：改回目前的，不要卡在上一版。
func TestPreviousFailingTooReturnsToStable(t *testing.T) {
	h := newHost(t)
	h.put(UpdaterName, "v3 exit=1")
	h.put(PreviousName, "v2 exit=1")
	for i := 0; i < FailureLimit; i++ {
		h.run("auto")
	}
	for i := 0; i < FailureLimit; i++ {
		if _, ran := h.run("auto"); ran[0] != PreviousName {
			t.Fatalf("fallback run %d: %v", i, ran)
		}
	}
	s := h.state()
	if s.Fallback != nil || s.Failures != 0 || !hasEvent(s, "fallback_ended") {
		t.Fatalf("%+v", s)
	}
	if _, ran := h.run("auto"); ran[0] != UpdaterName {
		t.Fatalf("not back to stable: %v", ran)
	}
}

// 新版自己連不上或驗不過索引時 auto 照樣結束碼 0（查詢失敗記在 state.json）。查詢連續失敗超過 72 小時就以上一版
// 試一次：上一版查得到就退回；上一版也查不到就是網路或伺服器，24 小時內不再試。
func TestProbeWithPreviousWhenChecksKeepFailing(t *testing.T) {
	for _, prevReaches := range []bool{true, false} {
		h := newHost(t)
		h.put(UpdaterName, "v3 exit=0")
		h.put(PreviousName, "v2 exit=0")
		lastOK := h.now.Add(-80 * time.Hour)
		h.view = &UpdateView{Mode: "auto", LastSuccess: lastOK, LastCheck: h.now, NextCheck: h.now, CheckFailures: 12}
		h.afterRun = func(name string) {
			if name == PreviousName && prevReaches {
				v := *h.view
				v.LastSuccess, v.CheckFailures = h.now, 0
				h.view = &v
			}
		}
		if _, ran := h.run("auto"); len(ran) != 1 || ran[0] != PreviousName {
			t.Fatalf("prevReaches=%v: probe did not run previous: %v", prevReaches, ran)
		}
		s := h.state()
		if prevReaches != (s.Fallback != nil) {
			t.Fatalf("prevReaches=%v: %+v", prevReaches, s)
		}
		if prevReaches {
			continue
		}
		if !hasEvent(s, "probe") {
			t.Fatalf("probe not recorded: %+v", s)
		}
		h.view.NextCheck = h.now
		if _, ran := h.run("auto"); ran[0] != UpdaterName {
			t.Fatalf("probed again within 24h: %v", ran)
		}
	}
}

// 查詢測試只在真的「一直失敗」時做：最近成功過、暫停中、還沒輪到查詢、engine 呼叫都不做。手動更新從 #340 起也在背景查詢，照樣測。
func TestProbeOnlyWhenChecksReallyKeepFailing(t *testing.T) {
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	failing := UpdateView{Mode: "auto", LastSuccess: now.Add(-80 * time.Hour), LastCheck: now, NextCheck: now, CheckFailures: 10}
	if !(&State{}).probeDue("auto", &failing, now) {
		t.Fatal("baseline should probe")
	}
	for name, mutate := range map[string]func(v *UpdateView){
		"recent success":  func(v *UpdateView) { v.LastSuccess = now.Add(-10 * time.Hour) },
		"never succeeded": func(v *UpdateView) { v.LastSuccess = time.Time{} },
		"paused":          func(v *UpdateView) { v.PausedUntil = now.Add(time.Hour) },
		"not due":         func(v *UpdateView) { v.NextCheck = now.Add(time.Hour) },
		"few failures":    func(v *UpdateView) { v.CheckFailures = 1 },
	} {
		v := failing
		mutate(&v)
		if (&State{}).probeDue("auto", &v, now) {
			t.Errorf("%s: probed", name)
		}
	}
	manual := failing
	manual.Mode = "manual"
	if !(&State{}).probeDue("auto", &manual, now) {
		t.Error("manual mode checks in the background since #340 and should probe like the others")
	}
	if (&State{}).probeDue("engine", &failing, now) || (&State{}).probeDue("auto", nil, now) {
		t.Error("engine call or missing state.json probed")
	}
}

// update\ 不存在（解除安裝後殘留的排程）：照常執行，不建目錄、不留紀錄。紀錄損毀時同樣照常執行、不覆寫。
func TestNoUpdateDirOrBrokenState(t *testing.T) {
	h := newHost(t)
	h.put(UpdaterName, "v3 exit=0")
	os.RemoveAll(filepath.Join(h.base, "update"))
	if code, ran := h.run("auto"); code != 0 || len(ran) != 1 {
		t.Fatalf("%d %v", code, ran)
	}
	if _, err := os.Stat(filepath.Join(h.base, "update")); !os.IsNotExist(err) {
		t.Fatal("update dir created")
	}
	os.MkdirAll(filepath.Join(h.base, "update"), 0o700)
	os.WriteFile(StatePath(h.base), []byte("{broken"), 0o600)
	h.put(PreviousName, "v2 exit=0")
	h.remove(UpdaterName)
	if code, ran := h.run("auto"); code != 0 || len(ran) != 1 || ran[0] != PreviousName {
		t.Fatalf("broken state, missing stable: %d %v", code, ran)
	}
	if b, _ := os.ReadFile(StatePath(h.base)); string(b) != "{broken" {
		t.Fatalf("broken state overwritten: %s", b)
	}
	// 較新的格式也不覆寫。
	newer, _ := json.Marshal(map[string]any{"schema": stateSchema + 1})
	os.WriteFile(StatePath(h.base), newer, 0o600)
	h.put(UpdaterName, "v3 exit=0")
	h.run("auto")
	if b, _ := os.ReadFile(StatePath(h.base)); string(b) != string(newer) {
		t.Fatalf("newer state overwritten: %s", b)
	}
}

func TestFailing(t *testing.T) {
	h := newHost(t)
	h.put(UpdaterName, "v3 exit=1")
	h.put(PreviousName, "v2 exit=0")
	hash := h.env().Hash(filepath.Join(h.base, "bin", UpdaterName))
	for i := 0; i < FailureLimit; i++ {
		if Failing(h.base, hash) {
			t.Fatalf("failing after %d", i)
		}
		h.run("auto")
	}
	if !Failing(h.base, hash) || Failing(h.base, "other") || Failing(h.base, "") {
		t.Fatal("Failing mismatch")
	}
}
