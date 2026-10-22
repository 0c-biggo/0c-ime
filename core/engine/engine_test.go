package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhconv"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func testEngine() *Engine {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, w := range []string{"吃", "痴"} {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄔ"), PRW: 1})
		sys.SetUni(w, 1e-3)
	}
	return New(lex, sys)
}

// 宿主設定的模糊音交給之後開的每個 session；缺少設定時全部關閉。
func TestOpenSessionCarriesFuzzyConfig(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).Fuzzy.Enabled() {
		t.Fatal("預設不得啟用任何模糊音")
	}
	e.Fuzzy = fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Select)
	id := e.OpenSession()
	if got := e.Session(id).Fuzzy; got.Key() != "c-ch=select" {
		t.Fatalf("session 的模糊音設定：%q", got.Key())
	}
}

// 使用者關閉「空白鍵選字」時，之後開的每個 session 都照設定；預設空白選字。
func TestOpenSessionCarriesSpaceSelection(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).SpaceInsertsSpace {
		t.Fatal("預設空白鍵選字")
	}
	e.SpaceInsertsSpace = true
	if !e.Session(e.OpenSession()).SpaceInsertsSpace {
		t.Fatal("session 沒有帶到關閉空白鍵選字的設定")
	}
}

// #83：使用者選倚天 41 鍵時，之後開的每個 session 都用倚天排列；預設標準排列。
func TestOpenSessionCarriesLayout(t *testing.T) {
	e := testEngine()
	if snap := e.Session(e.OpenSession()).Handle(proto.Request{Type: proto.Key, Key: "1"}); !snap.Handled {
		t.Fatal("預設標準排列：1 是 ㄅ")
	}
	e.Layout = layout.ETen
	if snap := e.Session(e.OpenSession()).Handle(proto.Request{Type: proto.Key, Key: "5"}); snap.Handled {
		t.Fatal("倚天排列：閒置時的 5 不是注音鍵，應交還應用程式")
	}
	s := e.Session(e.OpenSession())
	s.Handle(proto.Request{Type: proto.Key, Key: "1"})
	if snap := s.Handle(proto.Request{Type: proto.Key, Key: " "}); snap.Text() != "˙" {
		t.Fatalf("倚天排列：1 是輕聲，單按＋空白是 ˙（#135）：%q", snap.Text())
	}
}

// #135：閒置聲調鍵由輸入法處理，只有中英混打（標準排列）例外；倚天排列不混打。
func TestToneMarkKeysFollowMixedInputAndLayout(t *testing.T) {
	e := testEngine()
	if !e.ToneMarkKeys() {
		t.Fatal("預設沒開混打：聲調鍵由輸入法處理")
	}
	e.MixedInput = true
	if e.ToneMarkKeys() {
		t.Fatal("中英混打：聲調鍵照混打規則")
	}
	if e.Layout = layout.Standard; e.ToneMarkKeys() {
		t.Fatal("明確選標準排列＋混打")
	}
	if e.Layout = layout.ETen; !e.ToneMarkKeys() {
		t.Fatal("倚天排列停用混打，聲調鍵由輸入法處理")
	}
}

// #183：外殼查詢的閒置按鍵依排列與設定；許氏的數字不是注音鍵，交還應用程式。
func TestIdleKeysFollowLayout(t *testing.T) {
	e := testEngine()
	if got := e.IdleKeys(); !strings.Contains(got, "1") || !strings.Contains(got, "=") {
		t.Fatalf("標準排列：1 是 ㄅ、= 開 Emoji：%q", got)
	}
	e.Layout = layout.Hsu
	if got := e.IdleKeys(); strings.ContainsAny(got, "0123456789") || !strings.Contains(got, "a") || !strings.Contains(got, ",") {
		t.Fatalf("許氏：%q", got)
	}
	e.EmojiShortcut = "off"
	if strings.Contains(e.IdleKeys(), "=") {
		t.Fatal("關閉 Emoji 選單時 = 交還應用程式")
	}
}

// #133：使用者開啟「選字後游標移到下一個字」時，之後開的每個 session 都照設定；預設關閉。
func TestOpenSessionCarriesAdvanceAfterSelect(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).AdvanceAfterSelect {
		t.Fatal("預設選字後游標不動")
	}
	e.AdvanceAfterSelect = true
	if !e.Session(e.OpenSession()).AdvanceAfterSelect {
		t.Fatal("session 沒有帶到選字後游標後移的設定")
	}
}

// #118：使用者開啟「輸出簡體字」時，之後開的每個 session 都用同一個轉換器；預設送出繁體。
func TestOpenSessionCarriesSimplified(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).Simplified != nil {
		t.Fatal("預設送出繁體")
	}
	conv, err := zhconv.TW2S(zhconv.Data{CJKCompatibility: "\uF900\t豈\n", TWVariants: "着\t著\n", TWVariantsRevPhrases: "什么\t什麼\n", TSPhrases: "計畫\t计划\n", TSCharacters: "計\t计\n"})
	if err != nil {
		t.Fatal(err)
	}
	e.Simplified = conv
	if e.Session(e.OpenSession()).Simplified != conv {
		t.Fatal("session 沒有帶到簡體轉換器")
	}
}

// #112：使用者選的選字鍵帶到之後開的每個 session；預設空字串（1234567890）。
func TestOpenSessionCarriesSelectionKeys(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).SelectionKeys != "" {
		t.Fatal("預設 1234567890")
	}
	e.SelectionKeys = "1234qweras"
	if e.Session(e.OpenSession()).SelectionKeys != "1234qweras" {
		t.Fatal("session 沒有帶到選字鍵")
	}
}

// 以位元組協定走一遍：近音候選的附註出現在 JSON 快照裡；原音候選沒有 note 欄位。
func TestFuzzyNoteSurvivesTheByteProtocol(t *testing.T) {
	e := testEngine()
	e.Fuzzy = fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Select)
	id := e.OpenSession()
	for _, k := range []string{"h", " "} { // ㄘ（大千：h＋一聲）
		if _, err := e.Handle(id, []byte(`{"type":"key","key":"`+k+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.Handle(id, []byte(`{"type":"candidates","length":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var snap proto.Snapshot
	if err := json.Unmarshal(out, &snap); err != nil || snap.Candidates == nil || len(snap.Candidates.Items) < 2 {
		t.Fatalf("快照：%s %v", out, err)
	}
	items := snap.Candidates.Items
	if items[0].Note != "近音 ㄔ" || items[len(items)-1].Note != "" {
		t.Fatalf("附註：%+v", items)
	}
	var raw struct {
		Candidates struct {
			Items []map[string]any `json:"items"`
		} `json:"candidates"`
	}
	json.Unmarshal(out, &raw)
	last := raw.Candidates.Items[len(raw.Candidates.Items)-1]
	if _, has := last["note"]; has {
		t.Fatalf("沒有附註時不應輸出 note 欄位：%v", last)
	}
}

// #115：全部共用時，一個輸入框切到英文，其他輸入框（含新開的）下一個請求就是英文；外殼換輸入框時送的
// 「設成中文」改成回到共用的模式；macOS Caps Lock 的「設成英文」只影響那個輸入框。
func TestSharedModeAcrossSessions(t *testing.T) {
	e := testEngine()
	e.SharedMode = true
	a, b := e.OpenSession(), e.OpenSession()
	mode := func(id uint64, r proto.Request) string {
		snap, err := e.Do(id, r)
		if err != nil {
			t.Fatal(err)
		}
		return snap.Mode
	}
	if mode(a, proto.Request{Type: proto.Toggle}) != proto.ModeEnglish {
		t.Fatal("切到英文")
	}
	if mode(b, proto.Request{Type: proto.Key, Key: "a"}) != proto.ModeEnglish {
		t.Fatal("另一個輸入框跟上")
	}
	c := e.OpenSession()
	if mode(c, proto.Request{Type: proto.Mode, On: false}) != proto.ModeEnglish {
		t.Fatal("新輸入框的焦點重設回到共用的模式（英文）")
	}
	if mode(b, proto.Request{Type: proto.Toggle}) != proto.ModeZhuyin || mode(a, proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("切回注音也同步")
	}
	if mode(a, proto.Request{Type: proto.Mode, On: true}) != proto.ModeEnglish || mode(b, proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("Caps Lock 的設成英文只影響那個輸入框")
	}
	// #173 Caps Lock 開著時直接打英文：開啟只影響那個輸入框；關閉回到共用的模式（期間別的輸入框切到英文就是英文）。
	e3 := testEngine()
	e3.SharedMode, e3.CapsLockEnglish = true, true
	p, q := e3.OpenSession(), e3.OpenSession()
	mode3 := func(id uint64, r proto.Request) string {
		snap, err := e3.Do(id, r)
		if err != nil {
			t.Fatal(err)
		}
		return snap.Mode
	}
	if mode3(p, proto.Request{Type: proto.CapsLock, On: true}) != proto.ModeEnglish || mode3(q, proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("Caps Lock 的暫時英文只影響那個輸入框")
	}
	if mode3(p, proto.Request{Type: proto.CapsLock, On: false}) != proto.ModeZhuyin {
		t.Fatal("關 Caps Lock 回到共用的注音")
	}
	mode3(p, proto.Request{Type: proto.CapsLock, On: true})
	mode3(q, proto.Request{Type: proto.Key, Key: proto.KeyEscape})
	if mode3(q, proto.Request{Type: proto.Toggle}) != proto.ModeEnglish || mode3(p, proto.Request{Type: proto.CapsLock, On: false}) != proto.ModeEnglish {
		t.Fatal("暫時英文期間別的輸入框切到英文：關 Caps Lock 後是共用的英文")
	}
	// 沒有開全部共用：各自的模式，照外殼原本的做法。
	e2 := testEngine()
	x, y := e2.OpenSession(), e2.OpenSession()
	e2.Do(x, proto.Request{Type: proto.Toggle})
	if snap, _ := e2.Do(y, proto.Request{Type: proto.Key, Key: "j"}); snap.Mode != proto.ModeZhuyin {
		t.Fatal("各自的模式")
	}
}
