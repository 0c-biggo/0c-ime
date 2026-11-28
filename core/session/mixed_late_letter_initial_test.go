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

// lateLetterSession 收「數字鍵韻母（8 ㄚ、9 ㄞ、0 ㄢ）＋字母鍵聲母」兩種讀法都有字的音節，量級照 0.2.13 資料包（#364）。
// 數字後面的量詞（次、時）以中文數詞的 bigram 表示，同 numberScorer 的做法。
func lateLetterSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 2e-3}, {"你", "ㄋㄧˇ", 1e-3}, {"的", "ㄉㄜ˙", 1.6e-2}, {"第", "ㄉㄧˋ", 8e-4}, {"買", "ㄇㄞˇ", 3e-4}, {"好", "ㄏㄠˇ", 1e-3},
		{"檢", "ㄐㄧㄢˇ", 3e-5}, {"檢查", "ㄐㄧㄢˇ ㄔㄚˊ", 1.2e-4}, {"查", "ㄔㄚˊ", 1.8e-4}, {"持", "ㄔˊ", 5.4e-4},
		{"是", "ㄕˋ", 5e-3}, {"煞", "ㄕㄚˋ", 1e-5}, {"時", "ㄕˊ", 1.75e-3}, {"啥", "ㄕㄚˊ", 3.5e-5}, {"上午", "ㄕㄤˋ ㄨˇ", 1e-4},
		{"在", "ㄗㄞˋ", 6e-3}, {"自", "ㄗˋ", 1.8e-3}, {"才", "ㄘㄞˊ", 8e-4}, {"慈", "ㄘˊ", 5e-5},
		{"次", "ㄘˋ", 5e-4}, {"菜", "ㄘㄞˋ", 2.4e-4}, {"產", "ㄔㄢˇ", 6.8e-4}, {"尺", "ㄔˇ", 2e-5}, {"生", "ㄕㄥ", 1e-3}, {"生產", "ㄕㄥ ㄔㄢˇ", 2e-4},
		{"辣", "ㄌㄚˋ", 3e-5}, {"然", "ㄖㄢˊ", 7.7e-4}, {"差", "ㄔㄚ", 1e-4}, {"吃", "ㄔ", 5e-4}, {"子", "ㄗ˙", 8e-6}, {"災", "ㄗㄞ", 8.6e-5},
		{"一", "ㄧ", 8e-3}, {"兩", "ㄌㄧㄤˇ", 1e-3}, {"三", "ㄙㄢ", 1.5e-3}, {"五", "ㄨˇ", 8e-4}, {"八", "ㄅㄚ", 5e-4}, {"十", "ㄕˊ", 1e-3},
		{"二十", "ㄦˋ ㄕˊ", 1e-4}, {"百", "ㄅㄞˇ", 5e-4}, {"幾", "ㄐㄧˇ", 8e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	for _, n := range numberLefts {
		sys.SetBi(n, "次", 0.03)
		sys.SetBi(n, "時", 0.01)
	}
	sys.SetBi("我", "在", 0.02)
	sys.SetBi("我", "才", 5e-3)
	sys.SetBi("我", "查", 5e-4)
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"a", "hello"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #364：緊接中文，數字鍵上的韻母比字母鍵上的聲母早一鍵（查 是 t86，打成 8t6），照位置組字，不切成「8持」。
// 聲母自己就成音節（ㄔ ㄕ ㄗ ㄘ）才有兩種讀法；照位置組的字接在前文後面，比聲母自己的字接在數字後面常用時組字。
func TestMixedLateLetterInitialComposes(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"ru038t6", "檢查"}, {"ji38t6", "我查"}, {"ji39y4", "我在"}, {"ji39h6", "我才"}, {"g/ 0t3", "生產"},
		{"ru038t6ji3", "檢查我"}, {"ji39y4ru038t6", "我在檢查"},
	} {
		s := lateLetterSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #364：真的在打數字的照舊。量詞、單位接在數字後面比照位置組的字接在前文後面常用得多（第9次、上午8時、我8是），
// 或照位置組不成常用字（買8尺 的 ㄔㄚˇ）。
func TestMixedLateLetterInitialKeepsNumbers(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"2u49h4", "第9次"}, {"2k79h4", "的9次"}, {"g;4j38g6", "上午8時"}, {"ji38g4", "我8是"}, {"a938t3", "買8尺"},
		{"2u48h4", "第8次"}, // ㄘㄚˋ 沒有字
	} {
		s := lateLetterSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #364 只看緊接中文的兩鍵。句首、空白、英文、全形標點後面數字開頭的照舊是數字接注音（#249），緊接字面數字的（30次 的 0h4）、
// 輕聲找不到字改讀一聲的（9y7 的 ㄗㄞ˙）也照舊；聲母自己不成音節的本來就照位置組字（我辣、我然），接空白的一聲也是（我差）。
func TestMixedLateLetterInitialKeepsOtherContexts(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"8t6", "8持"}, {"9y4", "9自"}, {"9h6", "9慈"}, {"8x4", "8x4"},
		{"ji3 8t6", "我 8持"}, {"hello 9y4", "hello 9自"}, {"ji3<9y4", "我，9自"},
		{"ji330h4", "我30次"}, {"ji338t6", "我38持"}, {"ji39y7", "我9子"},
		{"ji38x4", "我辣"}, {"ji30b6", "我然"}, {"ji38t ", "我差"}, {"8t ", "8t "},
	} {
		s := lateLetterSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
	// 前一段已經送出（組字區是空的）跟句首一樣。
	s := lateLetterSession()
	mixedType(s, "ji3")
	if got := key(s, proto.KeyEnter).Commit; got != "我" {
		t.Fatalf("先送出的 我：%q", got)
	}
	mixedType(s, "8t6")
	if got := key(s, proto.KeyEnter).Commit; got != "8持" {
		t.Errorf("送出後再打 8t6：%q，要 8持", got)
	}
}

// #364＋#274：照位置組出的字上按 ↓，第二個是原按鍵，選了變回 8t6。
func TestMixedLateLetterInitialKeepsKeys(t *testing.T) {
	s := lateLetterSession()
	mixedType(s, "ji38t6")
	snap := key(s, proto.KeyDown)
	got := itemTexts(snap)
	if len(got) < 2 || got[0] != "查" || got[1] != "8t6" {
		t.Fatalf("我＋8t6 的 ↓：%q", got)
	}
	s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[slices.Index(got, "8t6")].ID})
	if commit := key(s, proto.KeyEnter).Commit; commit != "我8t6" {
		t.Errorf("選原按鍵後提交 %q", commit)
	}
}

// #364：混打關閉時不走這條規則，一般注音模式本來就照位置組字。
func TestMixedLateLetterInitialPlainModeUnchanged(t *testing.T) {
	s := lateLetterSession()
	s.MixedInput = false
	for _, k := range "ru038t6" {
		key(s, string(k))
	}
	if got := key(s, proto.KeyEnter).Commit; got != "檢查" {
		t.Errorf("一般注音模式 ru038t6：%q", got)
	}
}
