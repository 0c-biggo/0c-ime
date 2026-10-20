//go:build linux

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	core "git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhconv"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/simplified"
)

// liveSimplifiedEngine 是能即時切換「輸出簡體字」的引擎（#169）；saved 記下引擎請宿主寫進設定檔的值。
func liveSimplifiedEngine(t *testing.T) (*core.Engine, *[]bool) {
	eng := toyEngine(t)
	var saved []bool
	eng.SimplifiedSwitch = core.SimplifiedSwitch{Load: func() (*zhconv.Converter, error) { return simplified.TW2S() },
		Save: func(on bool) error { saved = append(saved, on); return nil }}
	return eng, &saved
}

// propertyUpdates 取出 UpdateProperty 訊號裡某個屬性最後一次的樣子。
func propertyUpdate(sig []recorded, key string) (ibusProperty, bool) {
	var found ibusProperty
	ok := false
	for _, s := range sig {
		if s.name != "UpdateProperty" {
			continue
		}
		if p := s.args[0].(dbus.Variant).Value().(ibusProperty); p.Key == key {
			found, ok = p, true
		}
	}
	return found, ok
}

// #169：選單的「輸出簡體字」是可以打勾的屬性。點了在這個輸入框的 session 送 output_simplified：組字不動、提示列顯示
// 「已切換為輸出簡體字」幾秒後換回原本的內容，注／英指示改成「注簡」，之後送出的字是簡體；不記進重播紀錄。
func TestSimplifiedToggleSwitchesLive(t *testing.T) {
	e, rec, sock := newTestEngine(t)
	eng, saved := liveSimplifiedEngine(t)
	defer serveEngine(t, sock, eng, map[string]string{"output_simplified_live": "true"})()
	old := simplifiedNoticeFor
	simplifiedNoticeFor = 50 * time.Millisecond
	defer func() { simplifiedNoticeFor = old }()
	svc := e.svc
	// 0c-update 0.2.1 起回報這項設定：選單出現、沒打勾。
	svc.update = parseStatus(`{"settings":{"output_simplified":false}}`)
	if p := svc.simplifiedProperty(); p.hidden || p.checked || p.typ != propToggle || p.label != "輸出簡體字" {
		t.Fatalf("選單項目：%+v", p)
	}
	if p := parseStatus(`{"installed":"0.2.0"}`); p.simplifiedKnown {
		t.Fatal("舊版 0c-update 沒有這項設定：不顯示")
	}
	svc.mu.Lock()
	typeKeys(e, "z/ ru/3") // ㄈㄥ ㄐㄧㄥˇ
	if e.preedit != "風景" {
		t.Fatalf("組字：%q", e.preedit)
	}
	rec.take()
	if _, err := svc.engineMethod(e, "PropertyActivate", []interface{}{propSimplified, uint32(propStateChecked)}); err != nil {
		t.Fatal(err)
	}
	sig := rec.take()
	if e.preedit != "風景" || e.aux != "已切換為輸出簡體字" || !svc.simplified || !slices.Equal(*saved, []bool{true}) {
		t.Fatalf("切換後：組字 %q、提示 %q、狀態 %v、寫檔 %v", e.preedit, e.aux, svc.simplified, *saved)
	}
	// GNOME Shell 的頂列只採用不到 3 個字的 symbol。
	if p, ok := propertyUpdate(sig, propInputMode); !ok || variantText(t, p.Symbol) != "注簡" || len([]rune(variantText(t, p.Symbol))) >= 3 {
		t.Fatalf("注／英指示：%+v", p)
	}
	if p, ok := propertyUpdate(sig, propSimplified); !ok || p.State != propStateChecked {
		t.Fatalf("選單打勾：%+v", p)
	}
	for _, r := range e.session.log {
		if r.Type == "output_simplified" {
			t.Fatal("切換不能記進重播紀錄")
		}
	}
	svc.mu.Unlock()
	// 提示幾秒後換回原本的提示列（這裡沒有候選，是空的）。
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		svc.mu.Lock()
		aux := e.aux
		svc.mu.Unlock()
		if aux == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("提示沒有收起：%q", aux)
		}
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	rec.take()
	e.processKey(keyReturn, 0, 0)
	if commits := commitTexts(t, rec.take()); !slices.Equal(commits, []string{"风景"}) {
		t.Fatalf("送出：%v", commits)
	}
	// 再點一次切回繁體：指示回到「注」，勾選拿掉。
	svc.engineMethod(e, "PropertyActivate", []interface{}{propSimplified, uint32(0)})
	sig = rec.take()
	if p, _ := propertyUpdate(sig, propInputMode); variantText(t, p.Symbol) != "注" {
		t.Fatalf("切回繁體：%+v", p)
	}
	if svc.simplified || !slices.Equal(*saved, []bool{true, false}) {
		t.Fatalf("%v %v", svc.simplified, *saved)
	}
	// 設定程式在別處切換：下一個快照就跟上（不必等狀態輪詢）。
	other := newSession(e.session.client, false)
	other.ensureOpen()
	other.outputSimplified(true)
	typeKeys(e, "j")
	if !svc.simplified {
		t.Fatal("別處切換後，下一個按鍵的快照應跟上")
	}
	// 英文模式的指示照舊是「英」。
	e.toggleMode()
	if e.modeProperty().symbol != "英" {
		t.Fatalf("英文模式：%q", e.modeProperty().symbol)
	}
}

// 候選有多頁時提示列是「提示　1/2」：提示照樣幾秒後收起，換回候選標題與頁碼（code review：只比對提示本身時永遠收不掉）。
func TestSimplifiedNoticeClearsWithCandidatePages(t *testing.T) {
	e, _, sock := newTestEngine(t)
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, w := range strings.Split("是事世市式室試視示士勢氏仕侍誓適釋飾", "") {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄕˋ"), PRW: 1})
		sys.SetUni(w, 1e-4)
	}
	eng := core.New(lex, sys)
	eng.SimplifiedSwitch = core.SimplifiedSwitch{Load: func() (*zhconv.Converter, error) { return simplified.TW2S() },
		Save: func(bool) error { return nil }}
	defer serveEngine(t, sock, eng, map[string]string{"output_simplified_live": "true"})()
	old := simplifiedNoticeFor
	simplifiedNoticeFor = 50 * time.Millisecond
	defer func() { simplifiedNoticeFor = old }()
	svc := e.svc
	svc.update = parseStatus(`{"settings":{"output_simplified":false}}`)
	svc.mu.Lock()
	typeKeys(e, "g4") // ㄕˋ
	e.processKey(keyDown, 0, 0)
	before := e.aux
	if !strings.HasSuffix(before, "　1/2") {
		svc.mu.Unlock()
		t.Fatalf("前置：候選應有兩頁，提示列 %q", before)
	}
	svc.engineMethod(e, "PropertyActivate", []interface{}{propSimplified, uint32(propStateChecked)})
	if e.aux != "已切換為輸出簡體字　1/2" {
		svc.mu.Unlock()
		t.Fatalf("切換後的提示列：%q", e.aux)
	}
	svc.mu.Unlock()
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		svc.mu.Lock()
		aux := e.aux
		svc.mu.Unlock()
		if aux == before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("提示沒有收起：%q，應換回 %q", aux, before)
		}
	}
}

// 舊引擎（沒有 output_simplified_live）：選單退回 0c-update set output-simplified on -apply -notify，由 0c-update 寫設定、
// 交接重啟套用並送桌面通知；勾選照狀態查詢的結果。
func TestSimplifiedToggleFallsBackToUpdater(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer serveEngine(t, sock, toyEngine(t), nil)()
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := filepath.Join(home, "calls")
	os.MkdirAll(filepath.Join(home, ".local/bin"), 0o755)
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\n[ \"$1\" = status ] && echo '{\"settings\":{\"output_simplified\":true}}'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(home, ".local/bin/0c-update"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := e.svc
	svc.mu.Lock()
	svc.update = parseStatus(`{"settings":{"output_simplified":false}}`)
	svc.engineMethod(e, "PropertyActivate", []interface{}{propSimplified, uint32(propStateChecked)})
	svc.mu.Unlock()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		svc.mu.Lock()
		done := svc.updateRunning == "" && !svc.querying
		on := svc.simplified
		svc.mu.Unlock()
		if done {
			if !on {
				t.Fatal("狀態查詢回來後應打勾")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("逾時")
		}
	}
	b, _ := os.ReadFile(calls)
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); lines[0] != "set output-simplified on -apply -notify" {
		t.Fatalf("calls = %q", b)
	}
}

func commitTexts(t *testing.T, sig []recorded) []string {
	t.Helper()
	var out []string
	for _, s := range sig {
		if s.name == "CommitText" {
			out = append(out, variantText(t, s.args[0]))
		}
	}
	return out
}
