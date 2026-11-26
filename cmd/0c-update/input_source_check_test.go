//go:build !windows

package main

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const (
	testParent = "com.biggo.inputmethod.0c"
	testMode   = "com.biggo.inputmethod.0c.Bopomofo"
)

func healthy() inputSourceHealth {
	return inputSourceHealth{Sources: []inputSourceState{
		{ID: testParent, Present: true, Enabled: true},
		{ID: testMode, Present: true, Enabled: true, SelectCapable: true},
	}}
}

func TestParseInputSourceHealth(t *testing.T) {
	// 0c --inspect-input-source 的輸出（InputSource.swift 的 inspect，JSONSerialization 排序過的鍵）。
	out := `{"sources":[{"enabled":true,"id":"com.biggo.inputmethod.0c","present":true,"select_capable":false,"selected":false},` +
		`{"enabled":true,"id":"com.biggo.inputmethod.0c.Bopomofo","present":true,"select_capable":true,"selected":true}]}` + "\n"
	h, err := parseInputSourceHealth([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := healthy()
	want.Sources[1].Selected = true
	if !reflect.DeepEqual(h, want) {
		t.Fatalf("got %+v", h)
	}
	if p := h.problem(); p != "" {
		t.Fatalf("父項不可選取是正常的（只有模式可選）：%q", p)
	}
	for _, bad := range []string{"", "{}", `{"sources":[]}`, "com.biggo.inputmethod.0c enabled"} {
		if _, err := parseInputSourceHealth([]byte(bad)); err == nil {
			t.Errorf("%q 應該看不懂", bad)
		}
	}
}

func TestInputSourceProblem(t *testing.T) {
	cases := []struct {
		name string
		edit func(*inputSourceHealth)
		want string
	}{
		{"正常", func(*inputSourceHealth) {}, ""},
		{"登記消失", func(h *inputSourceHealth) { h.Sources[1] = inputSourceState{ID: testMode} }, testMode + " missing"},
		{"模式停用", func(h *inputSourceHealth) { h.Sources[1].Enabled = false }, testMode + " disabled"},
		{"反灰", func(h *inputSourceHealth) { h.Sources[1].SelectCapable = false }, testMode + " not-selectable"},
		{"父項停用", func(h *inputSourceHealth) { h.Sources[0].Enabled = false }, testParent + " disabled"},
		{"整個消失", func(h *inputSourceHealth) {
			h.Sources = []inputSourceState{{ID: testParent}, {ID: testMode}}
		}, testParent + " missing, " + testMode + " missing"},
		{"空的", func(h *inputSourceHealth) { h.Sources = nil }, "no input source"},
	}
	for _, c := range cases {
		h := healthy()
		c.edit(&h)
		if got := h.problem(); got != c.want {
			t.Errorf("%s：got %q want %q", c.name, got, c.want)
		}
	}
}

// fakeTIS 模擬系統的輸入來源：repairBy 是哪一步之後恢復（register、menu，空字串為修不好）。
type fakeTIS struct {
	health   inputSourceHealth
	err      error
	repairBy string
	calls    []string
	waits    []time.Duration
}

func (f *fakeTIS) repair() inputSourceRepair {
	return inputSourceRepair{
		inspect: func() (inputSourceHealth, error) { f.calls = append(f.calls, "inspect"); return f.health, f.err },
		register: func() error {
			f.calls = append(f.calls, "register")
			if f.repairBy == "register" {
				f.health = healthy()
			}
			return nil
		},
		restartMenu: func() error {
			f.calls = append(f.calls, "menu")
			if f.repairBy == "menu" {
				f.health = healthy()
			}
			return nil
		},
		wait: func(d time.Duration) { f.waits = append(f.waits, d) },
	}
}

func TestInputSourceRepairSteps(t *testing.T) {
	broken := healthy()
	broken.Sources[1].SelectCapable = false

	// 正常：只查一次，不登記、不重啟 TextInputMenuAgent。
	f := &fakeTIS{health: healthy()}
	outcome, steps := f.repair().run()
	if outcome != "ok" || !reflect.DeepEqual(f.calls, []string{"inspect"}) || len(steps) != 1 || !steps[0].OK {
		t.Fatalf("正常：%s %v %+v", outcome, f.calls, steps)
	}

	// 反灰、重新登記後恢復：不重啟 TextInputMenuAgent。
	f = &fakeTIS{health: broken, repairBy: "register"}
	outcome, steps = f.repair().run()
	if outcome != "repaired_register" || !reflect.DeepEqual(f.calls, []string{"inspect", "register", "inspect"}) {
		t.Fatalf("重新登記：%s %v", outcome, f.calls)
	}
	if !reflect.DeepEqual(f.waits, []time.Duration{inputSourceSettle}) {
		t.Fatalf("重新登記後要等系統掃描完再查：%v", f.waits)
	}
	if steps[0].Detail != testMode+" not-selectable" || steps[0].OK || !steps[1].OK || steps[1].Step != "register" || !steps[2].OK {
		t.Fatalf("每一步都要記下：%+v", steps)
	}

	// 登記救不回、重啟 TextInputMenuAgent 後恢復。
	f = &fakeTIS{health: broken, repairBy: "menu"}
	outcome, steps = f.repair().run()
	if outcome != "repaired_menu_agent" || !reflect.DeepEqual(f.calls, []string{"inspect", "register", "inspect", "menu", "inspect"}) {
		t.Fatalf("重啟選單：%s %v", outcome, f.calls)
	}
	if len(steps) != 5 || steps[3].Step != "restart_menu_agent" || !steps[4].OK {
		t.Fatalf("%+v", steps)
	}

	// 都救不回。
	f = &fakeTIS{health: broken}
	if outcome, _ = f.repair().run(); outcome != "failed" || len(f.calls) != 5 {
		t.Fatalf("修不好：%s %v", outcome, f.calls)
	}

	// 查不到（工具失敗、逾時）：不修，不知道狀態就不動使用者的輸入來源。
	f = &fakeTIS{err: errors.New("timeout")}
	outcome, steps = f.repair().run()
	if outcome != "query_failed" || !reflect.DeepEqual(f.calls, []string{"inspect"}) || steps[0].Detail != "timeout" {
		t.Fatalf("查不到：%s %v %+v", outcome, f.calls, steps)
	}

	// 重新登記回報錯誤（例如等「允許」逾時）仍繼續下一步。
	f = &fakeTIS{health: broken, repairBy: "menu"}
	r := f.repair()
	register := r.register
	r.register = func() error { _ = register(); return errors.New("pending") }
	outcome, steps = r.run()
	if outcome != "repaired_menu_agent" || steps[1].OK || steps[1].Detail != "pending" {
		t.Fatalf("%s %+v", outcome, steps)
	}
}

func TestInputSourceCheckGate(t *testing.T) {
	ready := inputSourceCheckGate{Key: "0.2.15/42", Version: "0.2.15", SinceInstall: time.Minute}
	if w := ready.wait(); w != "" {
		t.Fatalf("條件都成立時要檢查：%q", w)
	}
	cases := []struct {
		want string
		edit func(*inputSourceCheckGate)
	}{
		{"checked", func(g *inputSourceCheckGate) { g.Last = &inputSourceCheck{Key: "0.2.15/42"} }},
		{"unsupported", func(g *inputSourceCheckGate) { g.Version = "0.2.14" }},
		{"unsupported", func(g *inputSourceCheckGate) { g.Version = "bundle 1" }},
		{"activation_pending", func(g *inputSourceCheckGate) { g.Pending = true }},
		{"relogin_required", func(g *inputSourceCheckGate) { g.Relogin = true }},
		{"settling", func(g *inputSourceCheckGate) { g.SinceInstall = 5 * time.Second }},
		{"handover_pending", func(g *inputSourceCheckGate) { g.ShellsLeft = true }},
		{"secure_input", func(g *inputSourceCheckGate) { g.SecureInput = true }},
	}
	for _, c := range cases {
		g := ready
		c.edit(&g)
		if got := g.wait(); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
	// 同一版重新安裝（App 換了 inode）、回復後再升級都要重查。
	g := ready
	g.Last = &inputSourceCheck{Key: "0.2.15/41"}
	if w := g.wait(); w != "" {
		t.Fatalf("換了 App 要重查：%q", w)
	}
	for _, v := range []string{"0.2.15-dev.1", "0.2.15", "0.2.16", "0.3.0-beta.1"} {
		if !inspectInputSourceSupported(v) {
			t.Errorf("%s 應有 --inspect-input-source", v)
		}
	}
}

func TestInputSourceCheckFile(t *testing.T) {
	base := t.TempDir()
	if loadInputSourceCheck(base) != nil {
		t.Fatal("沒有紀錄時為 nil")
	}
	c := inputSourceCheck{Key: "0.2.15/42", Version: "0.2.15", At: time.Date(2026, 11, 24, 1, 2, 3, 0, time.UTC), Outcome: "repaired_register",
		Steps: []inputSourceCheckStep{{Step: "query", Detail: testMode + " not-selectable"}, {Step: "register", OK: true}, {Step: "query", OK: true}}}
	saveInputSourceCheck(base, c)
	got := loadInputSourceCheck(base)
	if got == nil || !reflect.DeepEqual(*got, c) {
		t.Fatalf("got %+v", got)
	}
}

func TestInputSourceNotListed(t *testing.T) {
	// 0.2.15 起的 0c --inspect-input-source 另回報 listed（在 com.apple.inputsources 的啟用清單裡），enabled 已經考慮它（#348）。
	out := `{"sources":[{"enabled":true,"id":"com.biggo.inputmethod.0c","listed":true,"present":true,"select_capable":false,"selected":false},` +
		`{"enabled":false,"id":"com.biggo.inputmethod.0c.Bopomofo","listed":false,"present":true,"select_capable":true,"selected":false}]}` + "\n"
	h, err := parseInputSourceHealth([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if h.Sources[1].Listed == nil || *h.Sources[1].Listed || h.Sources[0].Listed == nil || !*h.Sources[0].Listed {
		t.Fatalf("listed：%+v", h)
	}
	if p := h.problem(); p != testMode+" not-listed" {
		t.Fatalf("problem %q", p)
	}
	// 讀不到清單（沒有這個鍵）時省略 listed，照 enabled 判斷。
	h.Sources[1].Listed = nil
	if p := h.problem(); p != testMode+" disabled" {
		t.Fatalf("problem %q", p)
	}
}

func TestLegacyModeMissing(t *testing.T) {
	yes, no := true, false
	parentOnly := inputSourceHealth{Sources: []inputSourceState{
		{ID: testParent, Present: true, Enabled: true, Listed: &yes},
		{ID: testMode, Present: true, Listed: &no},
	}}
	both := inputSourceHealth{Sources: []inputSourceState{
		{ID: testParent, Present: true, Enabled: true, Listed: &yes},
		{ID: testMode, Present: true, Enabled: true, Listed: &yes},
	}}
	none := inputSourceHealth{Sources: []inputSourceState{
		{ID: testParent, Present: true, Listed: &no},
		{ID: testMode, Present: true, Listed: &no},
	}}
	unknown := inputSourceHealth{Sources: []inputSourceState{
		{ID: testParent, Present: true, Enabled: true},
		{ID: testMode, Present: true},
	}}
	cases := []struct {
		name    string
		version string
		h       inputSourceHealth
		want    bool
	}{
		{"0.2.14 只允許了父項：換 App 前先補", "0.2.14", parentOnly, true},
		{"0.2.7 起的工具一次只送一個要求", "0.2.7", parentOnly, true},
		{"0.2.6 以前的工具會同時送出兩個要求，不用", "0.2.6", parentOnly, false},
		{"0.2.15 起模式預設啟用，補不進去", "0.2.15", parentOnly, false},
		{"0.2.15 開發版也是", "0.2.15-dev.348.2", parentOnly, false},
		{"兩個都在清單：不必補、不跳詢問", "0.2.14", both, false},
		{"都不在清單（使用者移除了）：交給新 App 只問一次", "0.2.14", none, false},
		{"讀不到清單：不補", "0.2.14", unknown, false},
		{"版本讀不到", "", parentOnly, false},
		{"沒有模式的舊 App", "0.2.14", inputSourceHealth{Sources: parentOnly.Sources[:1]}, false},
	}
	for _, c := range cases {
		if got := legacyModeMissing(c.version, c.h); got != c.want {
			t.Errorf("%s：got %v want %v", c.name, got, c.want)
		}
	}
}
