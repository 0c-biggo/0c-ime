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

// positionalKeysSession 收 #274 要用的讀音，每個讀音再補 12 個同音字，同音字超過一頁（真實資料包的 ㄅㄛˊ 有 149 個）。
func positionalKeysSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	add := func(w, r string, p float64) {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: 1})
		sys.SetUni(w, p)
	}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"博", "ㄅㄛˊ", 5e-4}, {"的", "ㄉㄜ˙", 1.6e-2}, {"定", "ㄉㄧㄥˋ", 4e-4}, {"外", "ㄨㄞˋ", 3e-4}, {"見", "ㄐㄧㄢˋ", 4e-4},
		{"我", "ㄨㄛˇ", 2e-3}, {"好", "ㄏㄠˇ", 1e-3}, {"至", "ㄓˋ", 5e-4}, {"跌", "ㄉㄧㄝ", 1e-4}, {"好的", "ㄏㄠˇ ㄉㄜ˙", 5e-4},
	} {
		add(e.w, e.r, e.p)
	}
	// 同音字用私用區字元當詞面，機率都比上面的字低，排在第一個之後
	next := rune(0xE000)
	for _, r := range []string{"ㄅㄛˊ", "ㄉㄜ˙", "ㄉㄧㄥˋ", "ㄨㄞˋ", "ㄐㄧㄢˋ", "ㄨㄛˇ", "ㄓˋ", "ㄉㄧㄝ"} {
		for i := range 12 {
			add(string(next), r, 1e-5/float64(i+1))
			next++
		}
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"a", "hello"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #274：中英混打照位置組字（按鍵不照聲母、介音、韻母的順序：#119 的 i16 博、k27 的，#256 的 u2/4 定、2/u4 定、
// 9j4 外、ur04 見、u2, 跌）時，在字上按 ↓，第一個仍是畫面上的字，原按鍵排第二個，同音字多也在第一頁，選了變回原按鍵。
func TestMixedPositionalKeysOnFirstPage(t *testing.T) {
	for _, tc := range []struct{ keys, first, raw, commit string }{
		{"i16", "博", "i16", "i16"}, {"k27", "的", "k27", "k27"}, {"u2/4", "定", "u2/4", "u2/4"}, {"2/u4", "定", "2/u4", "2/u4"},
		{"9j4", "外", "9j4", "9j4"}, {"ur04", "見", "ur04", "ur04"}, {"u2, ", "跌", "u2, ", "u2, "},
		{"ji3k27", "的", "k27", "我k27"}, // 前面有字也一樣
	} {
		s := positionalKeysSession()
		mixedType(s, tc.keys)
		snap := key(s, proto.KeyDown)
		got := itemTexts(snap)
		if len(got) < 2 || got[0] != tc.first || got[1] != tc.raw {
			t.Errorf("%q 按 ↓ 第一頁：%q，要第一個 %s、第二個 %q", tc.keys, got, tc.first, tc.raw)
			continue
		}
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[1].ID})
		if commit := key(s, proto.KeyEnter).Commit; commit != tc.commit {
			t.Errorf("%q 選原按鍵後提交 %q，要 %q", tc.keys, commit, tc.commit)
		}
	}
}

// #274：照位置組出的字在詞裡（cl3k27 的「好的」）時，↓ 的詞清單第二個是整串原按鍵，後面接的單字清單裡
// k27 也排單字段第二，都在第一頁。
func TestMixedPositionalKeysInWordOnFirstPage(t *testing.T) {
	s := positionalKeysSession()
	mixedType(s, "cl3k27")
	got := itemTexts(key(s, proto.KeyDown))
	if len(got) < 2 || got[0] != "好的" || got[1] != "cl3k27" {
		t.Fatalf("cl3k27 按 ↓：%q，要第一個 好的、第二個 cl3k27", got)
	}
	single := slices.Index(got, "的")
	if single < 0 || single+1 >= len(got) || got[single+1] != "k27" {
		t.Errorf("cl3k27 按 ↓ 第一頁的單字段：%q，要 的 後面緊接 k27", got)
	}
}

// #274 只動照位置組出的字：照順序打的混打（ji3 我、ru04 見）原按鍵照舊排在同音字後面，數字鍵出的字（54 至）照舊第二個。
func TestMixedOrderedKeysStayLast(t *testing.T) {
	for _, tc := range []struct {
		keys   string
		second bool
	}{{"ji3", false}, {"ru04", false}, {"54", true}} {
		s := positionalKeysSession()
		mixedType(s, tc.keys)
		snap := key(s, proto.KeyDown)
		got := itemTexts(snap)
		if second := len(got) > 1 && got[1] == tc.keys; second != tc.second {
			t.Errorf("%q 按 ↓：%q，原按鍵排第二：%v，要 %v", tc.keys, got, second, tc.second)
		}
	}
}
