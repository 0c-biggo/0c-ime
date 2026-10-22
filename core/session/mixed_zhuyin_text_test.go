package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 注音文（ㄏㄏ、我ㄉ天、ㄌ、ㄇ）在中英混打下的打法（#190 客戶回信、#208）。
func zhuyinTextSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"天", "ㄊㄧㄢ", 3e-3}, {"的", "ㄉㄜ˙", 2e-2}, {"吃", "ㄔ", 2e-3}, {"好", "ㄏㄠˇ", 5e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	return s
}

// #190：畫面顯示成注音、自己組不成字的單獨聲母（c ㄏ、x ㄌ）按空白，比照一般注音模式（#109）原樣留下注音，
// 空白當一聲吃掉：c␣c␣ 是「ㄏㄏ」，以前是「ㄏ ㄏ 」。再按一次空白才是空白。
func TestMixedLoneInitialSpaceKeepsZhuyin(t *testing.T) {
	s := zhuyinTextSession()
	if snap := keys(s, "c", " "); snap.Text() != "ㄏ" {
		t.Fatalf("c␣ 應為 ㄏ（不多空白）：%q", snap.Text())
	}
	if snap := keys(s, "c", " "); snap.Text() != "ㄏㄏ" {
		t.Fatalf("c␣c␣ 應為 ㄏㄏ：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "ㄏㄏ" {
		t.Fatalf("提交 %q", snap.Commit)
	}
	assertNoZhuyinLearned(t, s, "ㄏ")

	// 接在中文後面、後面再接中文：我ㄏㄏ天。
	s = zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "c", " ", "c", " ")
	if snap := keys(s, "w", "u", "0", " "); snap.Text() != "我ㄏㄏ天" {
		t.Fatalf("我ㄏㄏ天：%q", snap.Text())
	}

	// 要空白再按一次空白：x␣␣ 是「ㄌ 」。
	s = zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := keys(s, "x", " ", " "); snap.Text() != "我ㄌ " {
		t.Fatalf("x␣␣ 應為 ㄌ＋空白：%q", snap.Text())
	}

	// 留下的注音按 ↓：第一項是它，第二項是原按鍵（含空白，比照 #162 的 20␣），選了變回英文字母與空白。
	s = zhuyinTextSession()
	keys(s, "c", " ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	items := downItems(key(s, proto.KeyDown))
	if len(items) < 3 || items[0] != "ㄏ" || items[1] != "c " || items[2] != "ㄏˊ" {
		t.Fatalf("ㄏ 按 ↓ 應為 ㄏ、c␣、ㄏˊ…：%q", items)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != "c " {
		t.Fatalf("選原按鍵：%q", snap.Text())
	}

	// 不變：組得成字的一聲（t␣ 吃）、英文 a 照舊、英文字照舊有空白。
	s = zhuyinTextSession()
	if snap := keys(s, "t", " "); snap.Text() != "吃" {
		t.Fatalf("t␣ 照舊是吃：%q", snap.Text())
	}
	s = zhuyinTextSession()
	s.English = english.New([]english.Word{{Text: "a", Weight: 100}, {Text: "hello", Weight: 100}})
	if snap := keys(s, "a", " "); snap.Text() != "a " {
		t.Fatalf("a␣ 照舊是英文 a：%q", snap.Text())
	}
	if snap := keys(s, "h", "e", "l", "l", "o", " "); snap.Text() != "a hello " {
		t.Fatalf("英文字照舊接空白：%q", snap.Text())
	}
	// 照原樣顯示成數字的 2、1 不受影響（#162：1 ㄅ、2 ㄉ 自己沒有字，照舊是數字）。
	s = zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := keys(s, "2", " "); snap.Text() != "我2 " {
		t.Fatalf("我2␣ 照舊是數字：%q", snap.Text())
	}
}

// #208：↓ 選了注音解釋（2 → ㄉ）之後接著打下一個字，以前併成同一串按鍵照字面顯示（我2wu0），打不出「我ㄉ天」。
// 現在接著的鍵和選好的注音組不成同一個音節時，選好的注音原樣留下，這一鍵開始新的音節；接空白時空白當一聲吃掉。
func TestMixedPhoneticChoiceThenNextSyllable(t *testing.T) {
	s := zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "2")
	items := downItems(key(s, proto.KeyDown))
	if len(items) < 2 || items[0] != "2" || items[1] != "ㄉ" {
		t.Fatalf("2 按 ↓ 應為 2、ㄉ：%q", items)
	}
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap := keys(s, "w", "u", "0", " "); snap.Text() != "我ㄉ天" {
		t.Fatalf("我 2 ↓ㄉ wu0␣ 應為 我ㄉ天：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我ㄉ天" {
		t.Fatalf("提交 %q", snap.Commit)
	}

	// 選了 ㄉ 再按空白：不多空白。
	s = zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "2", proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap := key(s, " "); snap.Text() != "我ㄉ" {
		t.Fatalf("2 ↓ㄉ␣ 應為 我ㄉ：%q", snap.Text())
	}
	if snap := keys(s, "1", proto.KeyDown); len(downItems(snap)) < 2 || downItems(snap)[1] != "ㄅ" {
		t.Fatalf("1 按 ↓ 應有 ㄅ：%q", downItems(snap))
	}
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap := keys(s, "c", " "); snap.Text() != "我ㄉㄅㄏ" {
		t.Fatalf("我ㄉㄅㄏ：%q", snap.Text())
	}

	// 接著的鍵和選好的注音組得成音節時照舊接著組字（ㄉ＋ㄜ＋輕聲＝的）。
	s = zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "2", proto.KeyDown)
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if snap := keys(s, "k", "7"); snap.Text() != "我的" {
		t.Fatalf("2 ↓ㄉ k7 應為 我的：%q", snap.Text())
	}
}

// #208：照字面留下的單一注音鍵（我2␣ 的 2、句首的 - 與 1）按 ↓，第一項仍是原字，第二項是它的注音符號（比照 #153 的聲調鍵）。
// 屬於數字或英數的（2026、3.5、a1）不加。
func TestMixedLiteralPhoneticKeyDownOffersSymbol(t *testing.T) {
	s := zhuyinTextSession()
	typeSyl(s, "ㄨㄛˇ")
	keys(s, "2", " ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	items := downItems(key(s, proto.KeyDown))
	if len(items) < 2 || items[0] != "2" || items[1] != "ㄉ" {
		t.Fatalf("我2␣ 的 2 按 ↓ 應為 2、ㄉ：%q", items)
	}
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != "我ㄉ " {
		t.Fatalf("選 ㄉ：%q", snap.Text())
	}

	for k, want := range map[string]string{"1": "ㄅ", "-": "ㄦ", ",": "ㄝ"} {
		s := zhuyinTextSession()
		keys(s, k, " ")
		s.Handle(proto.Request{Type: proto.Cursor, To: 0})
		items := downItems(key(s, proto.KeyDown))
		if len(items) < 2 || items[0] != k || items[1] != want {
			t.Fatalf("句首 %s␣ 按 ↓ 應為 %s、%s：%q", k, k, want, items)
		}
	}

	// 數字的一部分不加。
	s = zhuyinTextSession()
	keys(s, "2", "0", "2", "6", " ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	if items := downItems(key(s, proto.KeyDown)); len(items) != 1 || items[0] != "2" {
		t.Fatalf("2026 的 2 不加注音：%q", items)
	}

	// 沒開混打不加（組字中的字面數字來自數字鍵盤）。
	s = zhuyinTextSession()
	s.MixedInput = false
	typeSyl(s, "ㄨㄛˇ")
	key(s, "KP2")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	if items := downItems(key(s, proto.KeyDown)); len(items) != 1 || items[0] != "2" {
		t.Fatalf("一般模式數字鍵盤的 2 不加注音：%q", items)
	}
}
