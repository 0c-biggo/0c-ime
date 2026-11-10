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

// knownAlnumSession 收 #249 後續三項要用的字，量級照 0.2.10 資料包：嘚 是 ㄉㄜ 唯一的字而且罕用。
func knownAlnumSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"班", "ㄅㄢ", 5e-4}, {"阿", "ㄚ", 3e-4}, {"埃", "ㄞ", 2e-5}, {"之", "ㄓ", 1e-3}, {"一", "ㄧ", 2e-3}, {"安", "ㄢ", 3e-4},
		{"嘚", "ㄉㄜ", 1e-7}, {"的", "ㄉㄜ˙", 1.6e-2}, {"呃", "ㄜ˙", 2e-5}, {"我", "ㄨㄛˇ", 2e-3},
		{"都", "ㄉㄡ", 2e-3}, {"至", "ㄓˋ", 5e-4}, {"少", "ㄕㄠˇ", 5e-4}, {"直", "ㄓˊ", 5e-4},
		{"站", "ㄓㄢˋ", 3e-4}, {"案", "ㄢˋ", 3e-4}, {"吧", "ㄅㄚ˙", 1e-3}, {"啊", "ㄚ˙", 1e-3}, {"辦", "ㄅㄢˋ", 3e-4},
		{"日", "ㄖˋ", 5e-4}, {"電", "ㄉㄧㄢˋ", 5e-4}, {"晏", "ㄧㄢˋ", 1e-5},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"a", "i", "win", "windows", "java", "gen", "line", "bug", "mini", "raid", "us", "top"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

func checkKnownAlnum(t *testing.T, cases []struct{ keys, want string }) {
	t.Helper()
	for _, tc := range cases {
		s := knownAlnumSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #249 第一項（2026-11-11 負責人決定）：真的存在的英數詞接空白照字面，不切成英文字＋一聲的字（win班、java阿、i埃、gen之）。
// 接聲調鍵的 usb4 也照字面（以前是 us日）。後面接著打的注音照常是中文。
func TestMixedKnownAlnumThenSpace(t *testing.T) {
	checkKnownAlnum(t, []struct{ keys, want string }{
		{"win10 ", "win10 "}, {"Win10 ", "Win10 "}, {"WIN10 ", "WIN10 "}, {"windows10 ", "windows10 "}, {"win8 ", "win8 "},
		{"java8 ", "java8 "}, {"i9 ", "i9 "}, {"gen5 ", "gen5 "}, {"raid0 ", "raid0 "}, {"top10 ", "top10 "}, {"TOP10 ", "TOP10 "},
		{"win10 ji3", "win10 我"}, {"usb4", "usb4"}, {"usb42k7", "usb4的"},
	})
}

// #249 第一項：不是英數詞的照現在的組法（Line之、BUG之一、mini安），A5、A8 照 #249 是 A之、A阿。
func TestMixedUnknownAlnumThenSpaceKeeps(t *testing.T) {
	checkKnownAlnum(t, []struct{ keys, want string }{
		{"line5 ", "line之"}, {"Line5 ", "Line之"}, {"bug5 u ", "bug之一"}, {"mini0 ", "mini安"}, {"A8 ", "A阿"}, {"A5 ", "A之"},
	})
}

// #249 第二項：字母與數字混打接空白組成的一聲只有罕用字時照字面（2k␣ 不是嘚）。打到一半的畫面（ㄉㄜ）不變，2k7 照舊是的。
func TestMixedRareAlnumSpaceLiteral(t *testing.T) {
	checkKnownAlnum(t, []struct{ keys, want string }{
		{"2k ", "2k "}, {"32k ", "32k "}, {"2k7", "的"}, {"2k ji3", "2k 我"},
	})
	s := knownAlnumSession()
	if got := mixedType(s, "2k").Text(); got != "ㄉㄜ" {
		t.Errorf("2k 打到一半顯示 %q，要照舊是 ㄉㄜ", got)
	}
}

// #249 第三項：前段是英數詞（DDR5、Q3）、整串不是，後面的數字鍵組成常用字就從詞的後面切開，↓ 第二個是原按鍵。
func TestMixedKnownAlnumThenDigitKeys(t *testing.T) {
	checkKnownAlnum(t, []struct{ keys, want string }{
		{"DDR52. ", "DDR5都"}, {"ddr52. ", "ddr5都"}, {"Q354gl3", "Q3至少"}, {"DDR52. ji3", "DDR5都我"},
	})
	s := knownAlnumSession()
	mixedType(s, "DDR52. ")
	if got := itemTexts(key(s, proto.KeyDown)); len(got) < 2 || got[0] != "都" || got[1] != "2. " {
		t.Errorf("DDR52.␣ 按 ↓：%q，要第一個 都、第二個原按鍵", got)
	}
}

// #249 第三項不切的：切出的字後面再接數字是更長的型號（DDR5＋5600），詞的最後一個數字鍵連後段也組得成字（B504 站、Q187 吧），
// #241 前段沒有數字的（RX104 辦），以 1 2 5 結尾的英數詞後面接數字鍵開頭的字（SATA的、PS電、Q的）照舊。
func TestMixedKnownAlnumThenDigitKeysKeeps(t *testing.T) {
	checkKnownAlnum(t, []struct{ keys, want string }{
		{"DDR55600", "DDR55600"}, {"F12 ", "F12 "}, {"RX104", "RX辦"},
		{"SATA2k7", "SATA的"}, {"PS2u04", "PS電"}, {"Q2k7", "Q2k7"},
	})
	for _, keys := range []string{"B504", "Q187"} {
		s := knownAlnumSession()
		mixedType(s, keys)
		if got := key(s, proto.KeyEnter).Commit; got == "B5案" || got == "Q1啊" {
			t.Errorf("%q：%q，詞的最後一個數字鍵連後段也組得成字時不從詞的後面切", keys, got)
		}
	}
}

// alnum.tsv 的格式與收錄原則：照字面的詞不能照順序打就是一個音節（a8 媽、g9 篩、m3 與），要用 keys 或不收。
func TestAlnumTokensAreNotInOrderSyllables(t *testing.T) {
	s := knownAlnumSession()
	tokens := english.AlnumTokens()
	if len(tokens) < 100 {
		t.Fatalf("alnum.tsv 只有 %d 個詞", len(tokens))
	}
	for tok, kind := range tokens {
		if kind != english.AlnumLiteral {
			continue
		}
		body := tok
		if sym, ok := s.layout.Symbol(rune(tok[len(tok)-1])); ok && zhuyin.ClassOf(sym) == zhuyin.Tone {
			body = tok[:len(tok)-1]
		}
		if _, ok := s.mixedComposer(body); ok && s.keysInSyllableOrder(body) {
			switch tok {
			case "a3", "a4", "q3", "q4", "r3", "r7", "r9", "am4": // ㄇˇ、ㄇˋ、ㄆˇ、ㄆˋ、ㄐˇ、ㄐ˙、ㄐㄞ、ㄇㄩˋ 沒有字
				continue
			case "x86": // ㄌㄚˊ 只有罕用字（#249）
				continue
			}
			t.Errorf("%q 照順序打就是注音 %q，不能是 literal", tok, body)
		}
	}
}

func TestParseAlnumRejectsBadLines(t *testing.T) {
	for _, src := range []string{"win10\tliteral\n", "win10\tliteral\t\n", "Win10\tliteral\tx\n", "win\tliteral\tx\n", "10\tliteral\tx\n",
		"win10\tbogus\tx\n", "win10\tliteral\tx\nwin10\tkeys\ty\n"} {
		if _, err := english.ParseAlnum(src); err == nil {
			t.Errorf("%q 應該是格式錯誤", src)
		}
	}
}
