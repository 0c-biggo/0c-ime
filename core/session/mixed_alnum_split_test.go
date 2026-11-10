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

// alnumSplitSession 收型號後面常接的字（#249），量級照真實資料包。
func alnumSplitSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"的", "ㄉㄜ˙", 1.6e-2}, {"得", "ㄉㄜ˙", 1e-4}, {"呃", "ㄜ˙", 2e-5}, {"我", "ㄨㄛˇ", 2e-3}, {"婐", "ㄨㄛˇ", 1e-6}, {"你", "ㄋㄧˇ", 2e-3}, {"是", "ㄕˋ", 5e-3}, {"施", "ㄕ", 2e-4},
		{"開", "ㄎㄞ", 6e-4}, {"成", "ㄔㄥˊ", 6e-4}, {"比", "ㄅㄧˇ", 6e-4}, {"以", "ㄧˇ", 1e-3}, {"被", "ㄅㄟˋ", 1.3e-3}, {"欸", "ㄟˋ", 2e-5},
		{"賺", "ㄓㄨㄢˋ", 2e-4}, {"萬", "ㄨㄢˋ", 5e-4}, {"補", "ㄅㄨˇ", 3e-4}, {"五", "ㄨˇ", 5e-4}, {"資", "ㄗ", 3e-4},
		{"不", "ㄅㄨˋ", 6e-3}, {"物", "ㄨˋ", 2e-4}, {"一", "ㄧ", 2e-3}, {"戳", "ㄔㄨㄛ", 1e-5},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
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

// #249：英文字母接數字的型號、代號（PS5、h264、Q3、m2、GTA6、mobile01、帳號 leehamin777）後面緊接著打的中文，
// 從數字裡或數字後面切開，不再整串變成英數：數字後面接字母鍵的後段，真的在打注音時只會是新的一個字。
// 數字鍵先算給中文（PS5 賺 是 PS5＋5j04，不是 PS55＋萬）。
func TestMixedChineseAfterAlnumToken(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"h2642k7", "h264的"}, {"HDR4002k7", "HDR400的"}, {"Q22k7", "Q2的"}, {"PS52k7", "PS5的"},
		{"h264ji3", "h264我"}, {"m2t/6", "m2成"}, {"Q3d9 ", "Q3開"}, {"gta6g4", "gta6是"}, {"mobile01d9 ", "mobile01開"},
		{"PS55j04", "PS5賺"}, {"FSR21j3", "FSR2補"}, {"Z8901o4", "Z890被"}, {"leehamin7772k7", "leehamin777的"},
		// 帳號、生日後面接著打中文也一樣（#119 原本整串照字面）
		{"nina87su3", "nina87你"}, {"lisa03su3", "lisa03你"},
	} {
		s := alnumSplitSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #249 不改的：字母後面的數字本身是中文的開頭（AI 的、YP 比），型號結尾的單一字母接空白（gpt4o␣、ps4u␣、S24u␣、Vm4630g␣），
// 只有數字的後段（RX104、DDR52.），型號本身（a87 嗎、mp3 照 #249 是允）。
func TestMixedAlnumTokenSplitKeeps(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"AI2k7", "AI的"}, {"YP1u3", "YP比"}, {"gpt4o ", "gpt4o "}, {"ps4u ", "ps4u "}, {"S24u ", "S24u "}, {"Vm4630g ", "Vm4630g "},
		{"gtx1080tj ", "gtx1080tj "}, {"u2", "u2"}, {"x86", "x86"},
	} {
		s := alnumSplitSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #249＋#274：型號後面數字鍵開頭的字（h264 的 2k7），前面的數字可能還是型號，↓ 第二個是原按鍵，比照 #241 的 RX104 辦；
// 字母開頭的（h264 我 的 ji3）照順序打，原按鍵照舊在後面。
func TestMixedDigitAfterAlnumKeysSecond(t *testing.T) {
	s := alnumSplitSession()
	mixedType(s, "h2642k7")
	if got := itemTexts(key(s, proto.KeyDown)); len(got) < 2 || got[0] != "的" || got[1] != "2k7" {
		t.Fatalf("h2642k7 按 ↓：%q，要第一個 的、第二個 2k7", got)
	}
	s.Handle(proto.Request{Type: proto.Select, Index: 1})
	if commit := key(s, proto.KeyEnter).Commit; commit != "h2642k7" {
		t.Errorf("選原按鍵後提交 %q", commit)
	}
	s = alnumSplitSession()
	mixedType(s, "h264ji3")
	if got := itemTexts(key(s, proto.KeyDown)); len(got) > 1 && got[1] == "ji3" {
		t.Errorf("h264ji3 按 ↓：%q，照順序打的原按鍵不必排第二", got)
	}
}
