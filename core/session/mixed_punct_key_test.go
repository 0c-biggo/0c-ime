package session

import (
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// punctKeySession：標準排列的標點鍵 . ㄡ、, ㄝ、; ㄤ、/ ㄥ、- ㄦ 一聲都有字（#187）。
func punctKeySession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"歐", "ㄡ", 3e-4}, {"洲", "ㄓㄡ", 1e-4}, {"周", "ㄓㄡ", 5e-4}, {"車", "ㄔㄜ", 5e-4},
		{"歐洲", "ㄡ ㄓㄡ", 3e-4}, {"歐洲車", "ㄡ ㄓㄡ ㄔㄜ", 1e-5}, {"欸", "ㄝ", 1e-4},
		{"骯", "ㄤ", 1e-5}, {"鞥", "ㄥ", 1e-7}, {"兒", "ㄦ", 1e-4}, {"之", "ㄓ", 1e-3},
		{"摟", "ㄌㄡ", 1e-4}, {"都", "ㄉㄡ", 1e-2}, {"公", "ㄍㄨㄥ", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	s.English = english.New([]english.Word{{Text: "hello", Weight: 100}, {Text: "world", Weight: 50},
		{Text: "vs", Weight: 40}, {Text: "max", Weight: 40}, {Text: "ma", Weight: 30}, {Text: "app", Weight: 40}})
	return s
}

// #187：句首或接在空白後的 . ㄡ、, ㄝ 單獨一鍵接空白，跟 #162 的 18 八一樣出一聲的字，要標點按 Enter。
// 其他情況照舊：; / 是程式碼與「A / B」的標點，- 是條列，3. 1. 是編號，緊接在英文字後面的 . 是句點。
func TestMixedPunctuationKeyWord(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{". ", "歐"},
		{". 5. tk ", "歐洲車"}, // 回報的「. 舟車」
		{", ", "欸"},
		{"hello . ", "hello 歐"},
		{"hello . 5. tk ", "hello 歐洲車"},
		{"ji3. 5. tk ", "我歐洲車"}, // 接在中文後面本來就對
		{"ji3 . ", "我 歐"},
		{"; ", "; "},
		{"/ ", "/ "},
		{"hello / world", "hello / world"},
		{"- ", "- "},
		{"- ji3", "- 我"},
		{"1. ", "1. "},
		{"3. ", "3. "},
		{"3, ", "3, "},
		{"10. ", "10. "},
		{".. ", ".. "},
		{"... ", "... "},
		{"./a ", "./a "},
	} {
		s := punctKeySession()
		snap := mixedType(s, tc.keys)
		if snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #187：Enter 照字面送出標點；出字後 ↓ 第二個候選是原按鍵，選了變回標點。
func TestMixedPunctuationKeyWordKeepsKey(t *testing.T) {
	s := punctKeySession()
	mixedType(s, ".")
	if got := key(s, proto.KeyEnter).Commit; got != "." {
		t.Fatalf(". Enter 提交 %q", got)
	}
	s = punctKeySession()
	mixedType(s, ". ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if snap.Candidates == nil || len(snap.Candidates.Items) < 2 || snap.Candidates.Items[0].Text != "歐" || snap.Candidates.Items[1].Text != ". " {
		t.Fatalf("候選：%+v", snap.Candidates)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != ". " {
		t.Fatalf("選回原按鍵：%q", snap.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != ". " {
		t.Fatalf("提交：%q", got)
	}
}

// #187：緊接在英文字後面的 . 是句點（hello 用 Tab 補完後打 .），空白照字面。
func TestMixedPunctuationKeyAfterEnglishWordStaysPunctuation(t *testing.T) {
	s := punctKeySession()
	s.EnglishAutocomplete = true
	mixedType(s, "hel")
	if snap := key(s, proto.KeyTab); snap.Text() != "hello" {
		t.Fatalf("補完：%q", snap.Text())
	}
	if snap := mixedType(s, ". "); snap.Text() != "hello. " {
		t.Fatalf("hello. 顯示 %q", snap.Text())
	}
}

// #188：直接打在英文字後面的標點鍵（hello. 、vs. ）是標點，接空白照字面，跟 #187 的「緊接在半形英數後面的 . 是句點」一致。
// 原本 #119 的英→中切法把 . 切給中文（hello歐、vs歐），連後面的空白也吃掉（say my name歐You're）；
// 先打空白再打 .（hello . 歐）、後段有數字或字母（app2. 都、hello5. 周）、明確的聲調鍵照舊組成中文。
func TestMixedPunctuationKeyRightAfterEnglishWord(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"hello. ", "hello. "},
		{"vs. ", "vs. "},
		{"hello, ", "hello, "},
		{"hello; ", "hello; "},
		{"hello/ ", "hello/ "},
		{"hello- ", "hello- "},
		{"hello.. ", "hello.. "},
		{"hello. world", "hello. world"},
		{"ji3hello. ", "我hello. "},
		{"max. ", "max. "}, // 不改成把英文字尾借去組字（ma摟）
		{"hello . ", "hello 歐"},
		{"hello5. ", "hello周"},
		{"app2. ", "app都"},
		{"helloej/ ", "hello公"},
		{"hello.ji3", "hello.我"},
	} {
		s := punctKeySession()
		snap := mixedType(s, tc.keys)
		if snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #187：打字時顯示空白會變成的讀音，跟 #162 的數字鍵一樣；不會變成中文的標點沒有提示。
func TestMixedPunctuationKeyWordHint(t *testing.T) {
	for _, tc := range []struct {
		keys string
		want string
	}{
		{".", "ㄡ"}, {",", "ㄝ"}, {"hello .", "ㄡ"}, {"ji3.", "ㄡ"},
		{";", ""}, {"/", ""}, {"-", ""}, {"3.", ""}, {"..", ""},
	} {
		s := punctKeySession()
		snap := mixedType(s, tc.keys)
		got := ""
		if snap.ReadingHint != nil {
			got = snap.ReadingHint.Text
		}
		if got != tc.want {
			t.Errorf("%q 讀音提示 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #190：混打時 Shift＋, Shift＋. 照舊出全形「，」「。」，按 ↓ 第二個候選是原按鍵 < >（比照 #162／#175／#187
// 的「↓ 第二項是原按鍵」），選了變回半形。句首、中文後面、空白後面都一樣；緊接在照字面的英數後面也一樣（#201：
// 以前是半形，我2026<、我OK<、我18>）。
func TestMixedShiftCommaPeriodDownOffersKey(t *testing.T) {
	for _, tc := range []struct{ keys, before, full, half string }{
		{"<", "", "，", "<"},
		{">", "", "。", ">"},
		{"ji3<", "我", "，", "<"},
		{"ji3>", "我", "。", ">"},
		{"ji3 <", "我 ", "，", "<"},
		{"ji32026<", "我2026", "，", "<"},
		{"ji3OK<", "我OK", "，", "<"},
		{"ji318>", "我18", "。", ">"},
		{"hello<", "hello", "，", "<"},
		{"ji3hello>", "我hello", "。", ">"},
		{"2026>", "2026", "。", ">"},
		{"A-<", "A-", "，", "<"},
	} {
		s := punctKeySession()
		if snap := mixedType(s, tc.keys); snap.Text() != tc.before+tc.full {
			t.Fatalf("%q 顯示 %q", tc.keys, snap.Text())
		}
		items := downItems(key(s, proto.KeyDown))
		if len(items) < 3 || items[0] != tc.full || items[1] != tc.half {
			t.Fatalf("%q 按 ↓：%q", tc.keys, items)
		}
		if items[2] != symbolAlternatives(tc.full)[1] {
			t.Fatalf("%q 按 ↓ 其餘照舊：%q", tc.keys, items)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != tc.before+tc.half {
			t.Fatalf("%q 選回原按鍵：%q", tc.keys, snap.Text())
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.before+tc.half {
			t.Fatalf("%q 提交：%q", tc.keys, got)
		}
	}
	// 前面再插字後游標移回「，」按 ↓，也找得到原按鍵，不會錯位。
	s := punctKeySession()
	mixedType(s, "ji3<ji3")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	mixedType(s, "ji3")
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	if items := downItems(s.Handle(proto.Request{Type: proto.Candidates, Length: 1})); len(items) < 2 || items[0] != "，" || items[1] != "<" {
		t.Fatalf("中間的「，」按 ↓：%q", items)
	}
}

// #190 的例外只限混打的 Shift＋, Shift＋.：沒開混打時（#177 標點維持全形）、Ctrl＋,（#168 明確要全形）、
// 其他標點鍵的 ↓ 照舊沒有半形。
func TestShiftCommaPeriodDownWithoutMixedUnchanged(t *testing.T) {
	plain := punctKeySession()
	plain.MixedInput = false
	typeSyl(plain, "ㄨㄛˇ")
	key(plain, "<")
	if items := downItems(key(plain, proto.KeyDown)); len(items) == 0 || slices.Contains(items, "<") {
		t.Fatalf("沒開混打時「，」的 ↓ 不附半形：%q", items)
	}
	for _, k := range []string{proto.KeyCtrlComma, "(", ":"} {
		s := punctKeySession()
		s.CtrlPunct = true
		mixedType(s, "ji3")
		key(s, k)
		items := downItems(key(s, proto.KeyDown))
		if len(items) == 0 || slices.ContainsFunc(items, func(t string) bool { return len(t) == 1 && t[0] < 0x80 }) {
			t.Fatalf("%s 的 ↓ 不附半形：%q", k, items)
		}
	}
}
