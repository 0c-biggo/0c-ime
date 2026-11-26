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

// englishDigitSession 收英文詞後面常接、以數字鍵（1 ㄅ、2 ㄉ、5 ㄓ）開頭的字，以及拿掉那個數字鍵後剩下的按鍵組成的字，
// 量級照真實資料包：剩下的（以、為、亦、萬、驗）常比數字鍵開頭的（比、對、必、賺、變）常用，但差不到 10 倍。
func englishDigitSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"對", "ㄉㄨㄟˋ", 1e-3}, {"為", "ㄨㄟˋ", 1.2e-3}, {"比", "ㄅㄧˇ", 6e-4}, {"以", "ㄧˇ", 1e-3},
		{"較", "ㄐㄧㄠˋ", 1e-4}, {"比較", "ㄅㄧˇ ㄐㄧㄠˋ", 3e-4}, {"必", "ㄅㄧˋ", 2e-4}, {"亦", "ㄧˋ", 2e-4},
		{"須", "ㄒㄩ", 1e-4}, {"必須", "ㄅㄧˋ ㄒㄩ", 2e-4}, {"賺", "ㄓㄨㄢˋ", 2e-4}, {"萬", "ㄨㄢˋ", 5e-4},
		{"變", "ㄅㄧㄢˋ", 4e-4}, {"驗", "ㄧㄢˋ", 1e-4}, {"及", "ㄐㄧˊ", 3e-4}, {"便", "ㄅㄧㄢˋ", 2e-4},
		{"的", "ㄉㄜ˙", 1.6e-2}, {"呃", "ㄜ˙", 2e-5}, {"都", "ㄉㄡ", 1e-3}, {"歐", "ㄡ", 5e-5},
		{"辦", "ㄅㄢˋ", 3e-4}, {"著", "ㄓㄨˋ", 1.5e-4}, {"物", "ㄨˋ", 2e-4}, {"之", "ㄓ", 1e-3}, {"一", "ㄧ", 2e-3}, {"你", "ㄋㄧˇ", 2e-3}, {"我", "ㄨㄛˇ", 2e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"ai", "gdp", "iphone", "etf", "apple", "google", "bike", "line", "win", "ps", "ddr", "rx", "hello", "a", "gta"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #359：認得的英文詞後面緊接數字鍵開頭的字，數字鍵先算給中文，不留在英文後面當型號
// （以前是 AI2為、GDP1以、apple1以、google5萬、bike1驗及：剩下的按鍵組成的字要常用 10 倍才讓）。
func TestMixedEnglishWordThenDigitInitial(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"AI2jo4", "AI對"},
		{"GDP1u3", "GDP比"},
		{"iPhone1u3rul4", "iPhone比較"},
		{"ETF1u4vm ", "ETF必須"},
		{"apple1u3", "apple比"},
		{"google5j04", "google賺"},
		{"bike1u04ru6", "bike變及"},
		{"hello2. ", "hello都"},
		// 英文字接數字的型號後面也一樣（gta5 是 alnum.tsv 的英數詞，以 5 結尾；跟 #249 的 PS5賺 同一條）
		{"gta55j4", "gta5著"}, {"PS55j04", "PS5賺"},
	} {
		s := englishDigitSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #359 不改的：英數詞（alnum.tsv）與 #249 的例子、英文接數字再接空白（#249 的清單規則）、
// 數字後面接字母的後段本身就是常用字（api2k7 本來就是 api的）、只有數字的後段（RX104）。
func TestMixedEnglishWordThenDigitKeeps(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"ps42k7", "ps4的"},
		{"DDR52. ", "DDR5都"},
		{"win10 ", "win10 "},
		{"RX104", "RX辦"},
		{"Line5 ", "Line之"},
		{"A5 ", "A之"},
		{"hello1u ", "hello1u "},
	} {
		s := englishDigitSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}
