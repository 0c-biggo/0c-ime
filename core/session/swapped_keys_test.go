package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// swappedKeysSession 收快打時常顛倒的音節（#256），和 #249 的數字接注音會用到的字；量級照真實資料包。
func swappedKeysSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 2e-3}, {"定", "ㄉㄧㄥˋ", 4e-4}, {"丁", "ㄉㄧㄥ", 5e-5}, {"見", "ㄐㄧㄢˋ", 4e-4}, {"建", "ㄐㄧㄢˋ", 2e-4},
		{"經", "ㄐㄧㄥ", 3e-4}, {"下", "ㄒㄧㄚˋ", 1e-3}, {"關", "ㄍㄨㄢ", 3e-4}, {"多", "ㄉㄨㄛ", 1e-3}, {"裝", "ㄓㄨㄤ", 1e-4},
		{"話", "ㄏㄨㄚˋ", 3e-4}, {"動", "ㄉㄨㄥˋ", 3e-4}, {"稟", "ㄅㄧㄣˇ", 1e-6}, {"品", "ㄆㄧㄣˇ", 3e-4}, {"丟", "ㄉㄧㄡ", 5e-5},
		{"演", "ㄧㄢˇ", 2e-4}, {"眼", "ㄧㄢˇ", 2e-4}, {"萬", "ㄨㄢˋ", 5e-4}, {"應", "ㄧㄥ", 4e-4},
		// #249 的數字接注音：照順序打的 20u4 是 20億，反序組得成的 電、變、亞、燕 也收進來
		{"一", "ㄧ", 2e-3}, {"億", "ㄧˋ", 5e-4}, {"點", "ㄉㄧㄢˇ", 5e-4}, {"顛", "ㄉㄧㄢ", 5e-5}, {"扁", "ㄅㄧㄢˇ", 5e-5}, {"意", "ㄧˋ", 2e-4}, {"以", "ㄧˇ", 1e-3}, {"物", "ㄨˋ", 2e-4}, {"度", "ㄉㄨˋ", 5e-4},
		{"電", "ㄉㄧㄢˋ", 5e-4}, {"變", "ㄅㄧㄢˋ", 3e-4}, {"亞", "ㄧㄚˋ", 1e-4}, {"燕", "ㄧㄢˋ", 3e-5}, {"段", "ㄉㄨㄢˋ", 2e-4},
		{"賺", "ㄓㄨㄢˋ", 2e-4}, {"五", "ㄨˇ", 5e-4}, {"轉", "ㄓㄨㄢˇ", 2e-4},
		{"外", "ㄨㄞˋ", 3e-4}, {"晚", "ㄨㄢˇ", 2e-4}, {"遠", "ㄩㄢˇ", 2e-4}, {"與", "ㄩˇ", 5e-4}, {"瓦", "ㄨㄚˇ", 3e-5},
		{"餘", "ㄩˊ", 2e-4}, {"元", "ㄩㄢˊ", 4e-4}, {"遇", "ㄩˋ", 1e-4}, {"院", "ㄩㄢˋ", 3e-4}, {"跌", "ㄉㄧㄝ", 1e-4}, {"別", "ㄅㄧㄝˊ", 5e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	var words []english.Word
	for _, w := range []string{"a", "hello"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #256：標準、倚天 41 鍵每個鍵只屬於一個位置，注音照位置組字（同新酷音、小麥注音）：快打時介音、韻母或聲母
// 晚一鍵到達，組成的音節和照順序打的一樣，組字區照聲母、介音、韻母的順序顯示。
func TestSwappedKeysComposeByPosition(t *testing.T) {
	for _, tc := range []struct {
		l          layout.Layout
		keys, want string
		shown      string // 聲調前的組字區
	}{
		{layout.Standard, "2/u4", "定", "ㄉㄧㄥ"}, {layout.Standard, "u2/4", "定", "ㄉㄧㄥ"},
		{layout.Standard, "ij3", "我", "ㄨㄛ"}, {layout.Standard, "0u3", "演", "ㄧㄢ"}, {layout.Standard, "/u ", "應", "ㄧㄥ"},
		{layout.Standard, "r0u4", "見", "ㄐㄧㄢ"}, {layout.Standard, "20u4", "電", "ㄉㄧㄢ"},
		{layout.ETen, "d-e4", "定", "ㄉㄧㄥ"}, {layout.ETen, "ox3", "我", "ㄨㄛ"},
		// 許氏同一鍵依狀態代表聲母或韻母：聲母打在前面時，介音、韻母顛倒照樣組得起來
		{layout.Hsu, "dlej", "定", "ㄉㄧㄥ"},
	} {
		s := swappedKeysSession()
		s.SetLayout(tc.l)
		body := tc.keys[:len(tc.keys)-1]
		if got := typeKeys(s, body).Text(); got != tc.shown {
			t.Errorf("%s %q 的組字區：%q，要 %q", tc.l.Name(), body, got, tc.shown)
		}
		key(s, tc.keys[len(tc.keys)-1:])
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%s %q：%q，要 %q", tc.l.Name(), tc.keys, got, tc.want)
		}
	}
}

// #256（客戶回報）：中英混打時，介音晚韻母一鍵（2/u4 是 ㄉㄥㄧˋ）照位置組字，不被 #249 當成英數寫法
// 切成「2/一」或照字面留下 r0u4。句首、中文後面、空白後面都一樣，組字區照聲母、介音、韻母的順序顯示。
func TestMixedSwappedVowelsCompose(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"2/u4", "定"}, {"2/u ", "丁"}, {"r0u4", "見"}, {"v8u4", "下"}, {"e0j ", "關"}, {"2ij ", "多"},
		{"5;j ", "裝"}, {"c8j4", "話"}, {"2/j4", "動"}, {"1pu3", "稟"}, {"2.u ", "丟"},
		{"ji32/u4", "我定"}, {"ji3r0u4", "我見"}, {"hello 2/u4", "hello 定"},
		// 聲母、韻母都是數字鍵、介音是 ㄨ：數字後面接 ㄨ 開頭的單音節字很少，照位置組字
		{"50j4", "賺"}, {"50j3", "轉"}, {"20j4", "段"}, {"ji350j4", "我賺"},
		// 沒有聲母、韻母先到（9j 是 ㄞㄨ、0j 是 ㄢㄨ），介音是 ㄨ、ㄩ 的照位置組字
		{"9j4", "外"}, {"0j4", "萬"}, {"0j3", "晚"}, {"0m3", "遠"}, {"8j3", "瓦"}, {"ji39j4", "我外"},
		// 聲母晚介音一鍵（u2/ 是 ㄧㄉㄥ、ur0 是 ㄧㄐㄢ）：句首、空白後也照位置組字
		{"u2/4", "定"}, {"ur04", "見"}, {"u2/ ", "丁"}, {"hello u2/4", "hello 定"}, {"u2, ", "跌"},
		// 照順序打的照舊
		{"2u/4", "定"}, {"ru04", "見"},
	} {
		s := swappedKeysSession()
		s.MixedInput = true
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
	for _, keys := range []string{"2/u", "u2/"} {
		s := swappedKeysSession()
		s.MixedInput = true
		if got := mixedType(s, keys).Text(); got != "ㄉㄧㄥ" {
			t.Errorf("%s 的組字區：%q，要 ㄉㄧㄥ", keys, got)
		}
	}
}

// #256：介音是 ㄧ、前面的聲母與韻母都是數字鍵的三鍵或兩鍵（20u 的 ㄉㄢㄧ、0u 的 ㄢㄧ）也照位置組字，跟常見注音輸入法的混打一樣：
// 20u4 是電、10u3 是扁、0u3 是演、8u4 是亞。要「20億」時打 20 按 Enter 再打億，或在電上按 ↓ 選回原按鍵。
func TestMixedSwappedVowelsBeforeYi(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"20u4", "電"}, {"10u4", "變"}, {"20u3", "點"}, {"20u ", "顛"}, {"0u3", "演"}, {"8u4", "亞"}, {"ji320u4", "我電"},
	} {
		s := swappedKeysSession()
		s.MixedInput = true
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #256 不改 #249 的數字接注音：整串組不成一個音節的數字（70u 的 7＋0u、100u、3.5u）、緊接在字面數字後面的兩鍵（40 餘的 4＋0m）
// 照舊是數字加音節；字母與數字順序顛倒、聲母晚到的兩鍵（u2、o1）照舊照字面。
func TestMixedSwappedVowelsKeepNumbers(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"70u4", "70億"}, {"100u4", "100億"}, {"30u4", "30億"}, {"720u4", "720億"}, {"ji3100u4", "我100億"},
		{"40m6", "40餘"}, {"30m4", "30遇"}, {"302j4", "30度"}, {"3.5u4", "3.5億"}, {"u2", "u2"}, {"o1 ", "o1 "},
	} {
		s := swappedKeysSession()
		s.MixedInput = true
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}
