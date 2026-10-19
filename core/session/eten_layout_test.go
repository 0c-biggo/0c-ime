package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// #83：倚天 41 鍵排列。按鍵依 layout.ETen 解讀；' 是 ㄘ、= 是 ㄦ，頓號用 \；
// 中英混打與 = 開 Emoji 選單依標準排列設計，倚天排列下停用。
func etenSession(t *testing.T) *Session {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"想", "ㄒㄧㄤˇ", 3e-3}, {"吃", "ㄔ", 2e-3}, {"是", "ㄕˋ", 1e-2},
		{"此", "ㄘˇ", 1e-3}, {"次", "ㄘˋ", 2e-3}, {"二", "ㄦˋ", 2e-3}, {"兒", "ㄦˊ", 1e-3}, {"而", "ㄦˊ", 2e-3},
		{"知", "ㄓ", 1e-3}, {"去", "ㄑㄩˋ", 2e-3}, {"字", "ㄗˋ", 1e-3}, {"的", "ㄉㄜ˙", 3e-2},
		{"風", "ㄈㄥ", 6e-4}, {"景", "ㄐㄧㄥˇ", 1e-3}, {"風景", "ㄈㄥ ㄐㄧㄥˇ", 2e-4},
		{"班", "ㄅㄢ", 5e-4}, {"嗯", "ㄣ", 1e-4}, {"昂", "ㄤˊ", 1e-4}, {"嘛", "ㄇㄚ˙", 5e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.SetLayout(layout.ETen)
	return s
}

func typeKeys(s *Session, keys string) proto.Snapshot {
	var snap proto.Snapshot
	for _, k := range keys {
		snap = key(s, string(k))
	}
	return snap
}

func typeETen(s *Session, syl string) proto.Snapshot {
	return typeKeys(s, layout.ETen.Keys(zhuyin.Syllable(syl)))
}

func TestETenTypesSyllables(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"xo3", "我"},          // ㄨ x、ㄛ o、ˇ 3
		{"ce03", "想"},         // ㄒ c、ㄧ e、ㄤ 0
		{". ", "吃"},           // ㄔ .、一聲空白
		{"/4", "是"},           // ㄕ /、ˋ 4
		{"'3", "此"},           // ' 是 ㄘ，不是頓號
		{"'4", "次"},           //
		{"=4", "二"},           // = 是 ㄦ，不開 Emoji 選單
		{", ", "知"},           // , 是 ㄓ
		{"7u4", "去"},          // 7 是 ㄑ、u 是 ㄩ
		{";4", "字"},           // ; 是 ㄗ
		{"dr1", "的"},          // 1 是輕聲
		{"f- ge-3", "風景"},     // - 是 ㄥ
		{"b8 ", "班"},          // 8 是 ㄢ
		{"9 ", "嗯"},           // 9 是 ㄣ
		{"02", "昂"},           // 0 是 ㄤ、2 是 ˊ
		{"ma1", "嘛"},          //
		{"xo3ce03. ", "我想吃"},  //
		{"xo3/4'3=4", "我是此二"}, //
	} {
		s := etenSession(t)
		if snap := typeKeys(s, c.keys); !snap.Handled || snap.Text() != c.want {
			t.Errorf("倚天 %q 應組出 %q，實際 %q（%+v）", c.keys, c.want, snap.Text(), snap)
		}
	}
}

func TestETenToneAndIdleKeys(t *testing.T) {
	s := etenSession(t)
	// 沒有組字時，不是注音鍵的 5、6 與空白交還應用程式；7 8 9 0 = ' 是注音鍵，開始組字。
	for _, k := range []string{"5", "6", " "} {
		if key(s, k).Handled {
			t.Errorf("倚天閒置時 %q 應交還應用程式", k)
		}
	}
	// 聲調鍵 1–4 先以數字留在組字區，緊接空白是聲調符號（#135，見 tone_mark_test.go）。
	for _, k := range []string{"1", "2", "3", "4"} {
		s := etenSession(t)
		if snap := key(s, k); !snap.Handled || snap.Text() != k || snap.Candidates != nil {
			t.Errorf("倚天閒置時聲調鍵 %q 留在組字區：%+v", k, snap)
		}
	}
	for _, k := range []string{"7", "8", "9", "0", "=", "'", ";", ",", ".", "/", "-"} {
		s := etenSession(t)
		if snap := key(s, k); !snap.Handled || !s.Composing() || snap.Candidates != nil {
			t.Errorf("倚天閒置時 %q 是注音鍵，應開始組字：%+v", k, snap)
		}
	}
	// 組字中 5、6 照字面留在組字區（同標準排列的落單聲調鍵）。
	s = etenSession(t)
	typeETen(s, "ㄨㄛˇ")
	if snap := key(s, "5"); !snap.Handled || snap.Text() != "我5" {
		t.Fatalf("組字中 5 照字面保留：%+v", snap)
	}
}

func TestETenPunctuation(t *testing.T) {
	s := etenSession(t)
	if snap := key(s, "\\"); !snap.Handled || snap.Text() != "、" {
		t.Fatalf("倚天的頓號用 \\：%+v", snap)
	}
	key(s, proto.KeyEscape)
	typeETen(s, "ㄨㄛˇ")
	for k, want := range map[string]string{"<": "，", ">": "。", "?": "？", ":": "：", "\"": "；", "[": "「", "]": "」"} {
		s := etenSession(t)
		typeETen(s, "ㄨㄛˇ")
		if snap := key(s, k); snap.Text() != "我"+want {
			t.Errorf("倚天 Shift 標點 %q 應為 %q：%q", k, want, snap.Text())
		}
	}
	// 組字中 ' 仍是 ㄘ：我＋此。
	s = etenSession(t)
	typeETen(s, "ㄨㄛˇ")
	if snap := typeKeys(s, "'3"); snap.Text() != "我此" {
		t.Fatalf("組字中的 ' 是 ㄘ：%q", snap.Text())
	}
}

func TestETenDisablesEmojiEquals(t *testing.T) {
	s := etenSession(t)
	s.EmojiShortcut = "equals"
	if snap := key(s, "="); snap.Candidates != nil || !snap.Handled || snap.Text() != "ㄦ" {
		t.Fatalf("倚天的 = 是 ㄦ，不開 Emoji 選單：%+v", snap)
	}
	if snap := key(s, "2"); snap.Text() != "而" && snap.Text() != "兒" {
		t.Fatalf("=2 組出 ㄦˊ：%q", snap.Text())
	}
	// 組字中也一樣。
	if snap := typeKeys(s, "=4"); snap.Candidates != nil || len([]rune(snap.Text())) != 2 {
		t.Fatalf("組字中的 = 也是 ㄦ：%+v", snap)
	}
}

func TestETenDisablesMixedInput(t *testing.T) {
	s := etenSession(t)
	s.MixedInput = true
	typeETen(s, "ㄨㄛˇ")
	if s.mixed.raw != "" {
		t.Fatalf("倚天排列不走混打暫存：%q", s.mixed.raw)
	}
	// 混打停用：注音打完後空白照一般注音模式開候選，不插入空白。
	if snap := key(s, " "); snap.Candidates == nil || snap.Text() != "我" {
		t.Fatalf("倚天下混打停用，空白開候選：%+v", snap)
	}
	key(s, proto.KeyEscape)
	// 英文字母照注音解讀，不當英文單字暫存。
	s = etenSession(t)
	s.MixedInput = true
	if snap := typeKeys(s, "/4"); snap.Text() != "是" || s.mixed.raw != "" {
		t.Fatalf("倚天下混打停用，/4 是「是」：%+v", snap)
	}
	if s.mixedInput() {
		t.Fatal("倚天排列下 mixedInput() 應為 false")
	}
}

// 切回標準排列：= 開 Emoji、混打照舊、' 是頓號。
func TestStandardLayoutUnchangedAfterSetLayout(t *testing.T) {
	s := newSession()
	s.SetLayout(layout.ETen)
	s.SetLayout(layout.Standard)
	s.EmojiShortcut = "equals"
	if snap := key(s, "="); snap.Candidates == nil {
		t.Fatalf("標準排列的 = 開 Emoji 選單：%+v", snap)
	}
	key(s, proto.KeyEscape)
	if snap := key(s, "'"); snap.Text() != "、" {
		t.Fatalf("標準排列的 ' 是頓號：%+v", snap)
	}
	key(s, proto.KeyEscape)
	s.MixedInput = true
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, " "); snap.Candidates != nil || snap.Text() != "我 " {
		t.Fatalf("標準排列的混打照舊插入空白：%+v", snap)
	}
	s.SetLayout(nil)
	if s.layout != layout.Standard {
		t.Fatal("nil 排列應為標準排列")
	}
}
