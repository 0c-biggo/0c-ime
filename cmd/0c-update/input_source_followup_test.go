//go:build !windows

package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

// #348：系統設定慢的時候，父項的「允許」在安裝等待的 60 秒之後才出現。使用者按了之後只有父項啟用，模式從沒送出要求，
// 選單裡沒有靈犀注音，要等重新登入。補做在同一個登入工作階段裡接手：父項一啟用就請系統啟用模式。

const (
	parentID = "com.biggo.inputmethod.0c"
	modeID   = "com.biggo.inputmethod.0c.Bopomofo"
)

func health(parent, mode bool) inputSourceHealth {
	return inputSourceHealth{Sources: []inputSourceState{
		{ID: parentID, Present: true, Enabled: parent},
		{ID: modeID, Present: true, Enabled: mode, SelectCapable: mode},
	}}
}

func TestFollowUpStep(t *testing.T) {
	cases := []struct {
		name string
		h    inputSourceHealth
		want followUpAction
	}{
		{"父項還沒允許", health(false, false), followUpWait},
		{"父項晚一點允許了、模式沒要求過", health(true, false), followUpRequestMode},
		{"都啟用了", health(true, true), followUpDone},
		{"模式已啟用（父項的狀態以模式為準）", health(false, true), followUpDone},
		{"沒有模式的舊 App：要求在安裝時已送出，只等", inputSourceHealth{Sources: []inputSourceState{{ID: parentID, Present: true}}}, followUpWait},
		{"沒有模式的舊 App 已啟用", inputSourceHealth{Sources: []inputSourceState{{ID: parentID, Present: true, Enabled: true}}}, followUpDone},
		{"父項不在清單（登記被清掉）", inputSourceHealth{Sources: []inputSourceState{{ID: parentID, Enabled: true}, {ID: modeID, Present: true}}}, followUpWait},
		{"查詢結果是空的", inputSourceHealth{}, followUpWait},
	}
	for _, c := range cases {
		if got := followUpStep(c.h); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// followUpSim 模擬系統：states 依序是每次查詢看到的狀態（用完後停在最後一個）；activate 回傳 activateErr。
type followUpSim struct {
	states      []inputSourceHealth
	inspectErr  error
	queries     int
	activations int
	activateErr error
	// afterActivate：activate 之後系統的狀態（例如按了模式的「允許」）；nil 時不變。
	afterActivate *inputSourceHealth
	requested     bool
	finished      int
	clock         time.Time
}

func (s *followUpSim) followUp(until time.Duration) inputSourceFollowUp {
	start := s.clock
	return inputSourceFollowUp{
		inspect: func() (inputSourceHealth, error) {
			s.queries++
			if s.inspectErr != nil {
				return inputSourceHealth{}, s.inspectErr
			}
			h := s.states[0]
			if len(s.states) > 1 {
				s.states = s.states[1:]
			}
			return h, nil
		},
		activate: func() error {
			s.activations++
			if s.afterActivate != nil {
				s.states = []inputSourceHealth{*s.afterActivate}
			}
			return s.activateErr
		},
		requested:     func() bool { return s.requested },
		markRequested: func() { s.requested = true },
		finish:        func() { s.finished++ },
		now:           func() time.Time { return s.clock },
		wait:          func(d time.Duration) { s.clock = s.clock.Add(d) },
		until:         start.Add(until),
		poll:          inputSourceFollowUpPoll,
	}
}

func TestFollowUpRequestsModeOnceTheParentIsAllowedLate(t *testing.T) {
	// 安裝放棄後 70 秒父項的「允許」才出現，使用者按下：下一次查詢就送出模式的要求，允許後完成。
	states := []inputSourceHealth{}
	for i := 0; i < 35; i++ {
		states = append(states, health(false, false))
	}
	states = append(states, health(true, false))
	done := health(true, true)
	s := &followUpSim{states: states, afterActivate: &done}
	if got := s.followUp(inputSourceFollowUpWatch).run(); got != "enabled" {
		t.Fatalf("outcome %q", got)
	}
	if s.activations != 1 || s.finished != 1 || !s.requested {
		t.Fatalf("activations=%d finished=%d requested=%v", s.activations, s.finished, s.requested)
	}
	if s.queries != 36 {
		t.Fatalf("queries=%d", s.queries)
	}
}

func TestFollowUpNeverResendsTheParentRequest(t *testing.T) {
	// 使用者按了「不允許」或一直沒理父項的詢問：只查到時限，不送任何要求，不會一直跳詢問。
	s := &followUpSim{states: []inputSourceHealth{health(false, false)}}
	if got := s.followUp(inputSourceFollowUpWatch).run(); got != "waiting" {
		t.Fatalf("outcome %q", got)
	}
	if s.activations != 0 || s.finished != 0 || s.requested {
		t.Fatalf("activations=%d finished=%d requested=%v", s.activations, s.finished, s.requested)
	}
	if want := int(inputSourceFollowUpWatch/inputSourceFollowUpPoll) + 1; s.queries != want {
		t.Fatalf("queries=%d, want %d", s.queries, want)
	}
}

func TestFollowUpModeAllowedAfterTheToolGaveUp(t *testing.T) {
	// 模式的詢問也晚出現：工具 60 秒後放棄，要求已送出；之後才按「允許」照樣生效，補做繼續查到，不再送第二次。
	states := []inputSourceHealth{health(true, false), health(true, false), health(true, false), health(true, true)}
	s := &followUpSim{states: states, activateErr: errors.New("已要求啟用 Bopomofo，但 60 秒內系統尚未啟用 (exit status 2)")}
	if got := s.followUp(inputSourceFollowUpWatch).run(); got != "enabled" {
		t.Fatalf("outcome %q", got)
	}
	if s.activations != 1 || s.finished != 1 {
		t.Fatalf("activations=%d finished=%d", s.activations, s.finished)
	}
}

func TestFollowUpModeDeclinedStaysForLogin(t *testing.T) {
	s := &followUpSim{states: []inputSourceHealth{health(true, false)}, activateErr: errors.New("exit status 2")}
	if got := s.followUp(inputSourceFollowUpWatch).run(); got != "requested" {
		t.Fatalf("outcome %q", got)
	}
	if s.activations != 1 || s.finished != 0 {
		t.Fatalf("activations=%d finished=%d", s.activations, s.finished)
	}
	// 之後的排程喚起（同一個工作階段）：模式的要求已送過，不再跳詢問。
	again := s.followUp(0)
	if got := again.run(); got != "waiting" || s.activations != 1 {
		t.Fatalf("outcome %q activations=%d", got, s.activations)
	}
}

func TestFollowUpInlineChecksOnce(t *testing.T) {
	s := &followUpSim{states: []inputSourceHealth{health(false, false)}}
	if got := s.followUp(0).run(); got != "waiting" || s.queries != 1 {
		t.Fatalf("outcome %q queries=%d", got, s.queries)
	}
	// 父項在這之前已經允許：這次喚起就送出模式的要求。
	done := health(true, true)
	s = &followUpSim{states: []inputSourceHealth{health(true, false)}, afterActivate: &done}
	if got := s.followUp(0).run(); got != "enabled" || s.activations != 1 || s.finished != 1 {
		t.Fatalf("outcome %q activations=%d finished=%d", got, s.activations, s.finished)
	}
}

func TestFollowUpQueryFailureDoesNotTouchTheInputSource(t *testing.T) {
	s := &followUpSim{inspectErr: errors.New("查詢輸入來源失敗")}
	if got := s.followUp(10 * time.Second).run(); got != "waiting" || s.activations != 0 || s.finished != 0 {
		t.Fatalf("outcome %q activations=%d finished=%d", got, s.activations, s.finished)
	}
}

func TestPlanFollowUp(t *testing.T) {
	cases := []struct {
		since time.Duration
		want  followUpPlan
	}{
		{-time.Minute, followUpNone}, // 時鐘往回跳：不猜
		{0, followUpSpawn},
		{29 * time.Minute, followUpSpawn},
		{inputSourceFollowUpWatch, followUpInline},
		{23 * time.Hour, followUpInline},
		{inputSourceFollowUpWindow, followUpNone},
	}
	for _, c := range cases {
		if got := planFollowUp(c.since); got != c.want {
			t.Errorf("since %v: got %v, want %v", c.since, got, c.want)
		}
	}
}

func TestModeRequestedIsPerLoginSession(t *testing.T) {
	base := t.TempDir()
	t0 := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	boot := t0.Add(-time.Hour)
	if modeRequestedIn(base, loginStamp{ASID: 7, At: t0}, boot) {
		t.Fatal("nothing recorded yet")
	}
	saveModeRequested(base, loginStamp{ASID: 7, At: t0})
	if !modeRequestedIn(base, loginStamp{ASID: 7, At: t0.Add(time.Hour)}, boot) {
		t.Fatal("same session")
	}
	if modeRequestedIn(base, loginStamp{ASID: 8, At: t0.Add(time.Hour)}, boot) {
		t.Fatal("a new login session asks again (the login retry)")
	}
	if modeRequestedIn(base, loginStamp{ASID: 7, At: t0.Add(2 * time.Hour)}, t0.Add(time.Hour)) {
		t.Fatal("rebooted since")
	}
	clearInputSource(base)
	if onDisk(inputSourceFollowUpFile(base)) {
		t.Fatal("clearInputSource keeps the follow-up record")
	}
}

func TestFinishFollowUpRelogin(t *testing.T) {
	f := newRetryFixture(t)
	write := func() {
		markReloginForTest(t, f.base)
		saveModeRequested(f.base, loginStamp{ASID: 7, At: f.t0})
	}
	write()
	finishInputSourceFollowUp(f.base, &inputSourceRetry{ASID: 7, At: f.t0})
	if f.pending() || onDisk(reloginMarker(f.base)) || onDisk(inputSourceFollowUpFile(f.base)) {
		t.Fatal("enabled: same as enabling at install time, no relogin")
	}
	if err := os.WriteFile(inputSourceMarker(f.base), []byte("x\ny\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write()
	finishInputSourceFollowUp(f.base, &inputSourceRetry{ASID: 7, At: f.t0, KeepRelogin: true})
	if f.pending() || !onDisk(reloginMarker(f.base)) {
		t.Fatal("signature changed: still needs relogin")
	}
}

func markReloginForTest(t *testing.T, base string) {
	t.Helper()
	if err := os.WriteFile(reloginMarker(base), formatReloginMarker(loginStamp{ASID: 7}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// retryInputSource 只在背景排程、安裝的同一個登入工作階段、還沒停止補做時交給補做；跨過登入邊界照舊補做一次。
func TestRetryInputSourceHandsTheSameSessionToFollowUp(t *testing.T) {
	saved := followUpInputSource
	defer func() { followUpInputSource = saved }()
	var sinces []time.Duration
	handled := true
	followUpInputSource = func(_, _, _ string, since time.Duration) bool {
		sinces = append(sinces, since)
		return handled
	}
	f := newRetryFixture(t)
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 7, At: f.t0})
	if err := f.run(7, 1); err != nil || f.calls != 0 || f.queries != 0 {
		t.Fatalf("err=%v calls=%d queries=%d", err, f.calls, f.queries)
	}
	if len(sinces) != 1 || sinces[0] != time.Minute {
		t.Fatalf("since=%v", sinces)
	}
	// 設定程式「立即安裝」、選單等著結果的呼叫不做。
	f.query = false
	_ = f.run(7, 16)
	if len(sinces) != 1 {
		t.Fatalf("non-scheduled run followed up: %v", sinces)
	}
	f.query = true
	// 補做不在時間內（回 false）：照舊每個工作階段查一次。
	handled = false
	_ = f.run(7, 25*60)
	if f.queries != 1 || f.calls != 0 {
		t.Fatalf("queries=%d calls=%d", f.queries, f.calls)
	}
	// 重新登入：照舊補做（--activate-input-source），不經過補做。
	n := len(sinces)
	f.fail = errors.New("exit status 2")
	_ = f.run(8, 26*60)
	if f.calls != 1 || len(sinces) != n {
		t.Fatalf("calls=%d follow-ups=%d", f.calls, len(sinces)-n)
	}
	// 停止補做之後不再交給補做。
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 8, At: f.t0.Add(26 * time.Hour), Failed: inputSourceRetrySessions})
	_ = f.run(8, 26*60+15)
	if len(sinces) != n {
		t.Fatalf("stopped retry followed up")
	}
}

func TestFollowUpDefaultModeComesWithTheParent(t *testing.T) {
	// 0.2.15 起模式預設啟用：父項的「允許」晚於安裝的 60 秒才按下，系統把父項與模式一起寫進啟用清單，補做查到就完成，
	// 不送模式的要求、不再問第二次。
	states := []inputSourceHealth{health(false, false), health(false, false), health(true, true)}
	s := &followUpSim{states: states}
	if got := s.followUp(inputSourceFollowUpWatch).run(); got != "enabled" {
		t.Fatalf("outcome %q", got)
	}
	if s.activations != 0 || s.finished != 1 || s.requested {
		t.Fatalf("activations=%d finished=%d requested=%v", s.activations, s.finished, s.requested)
	}
}

func TestFollowUpModeNotListedIsNotDone(t *testing.T) {
	// TIS 對預設啟用的模式一直回報已啟用；不在啟用清單裡時 0c --inspect-input-source 的 enabled 是 false（listed=false），
	// 補做不能當成完成（#348）。
	no, yes := false, true
	h := inputSourceHealth{Sources: []inputSourceState{
		{ID: parentID, Present: true, Enabled: true, Listed: &yes},
		{ID: modeID, Present: true, Enabled: false, Listed: &no, SelectCapable: true},
	}}
	if got := followUpStep(h); got != followUpRequestMode {
		t.Fatalf("got %v", got)
	}
	s := &followUpSim{states: []inputSourceHealth{h}, activateErr: errors.New("不在系統的輸入方式清單裡，無法自動加入")}
	if got := s.followUp(0).run(); got != "requested" || s.finished != 0 {
		t.Fatalf("outcome %q finished=%d", got, s.finished)
	}
}
