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

// alnumTokenSession 照真實資料包的量級收字（#249）。
func alnumTokenSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 2e-3}, {"的", "ㄉㄜ˙", 1.6e-2}, {"個", "ㄍㄜ˙", 2e-3}, {"天", "ㄊㄧㄢ", 1e-3}, {"手", "ㄕㄡˇ", 5e-4},
		{"運", "ㄩㄣˋ", 4e-4}, {"韻", "ㄩㄣˋ", 2e-5}, {"動", "ㄉㄨㄥˋ", 3e-4}, {"運動", "ㄩㄣˋ ㄉㄨㄥˋ", 3e-4},
		{"允", "ㄩㄣˇ", 4e-5}, {"隕", "ㄩㄣˇ", 2e-6}, {"許", "ㄒㄩˇ", 2e-4}, {"允許", "ㄩㄣˇ ㄒㄩˇ", 2e-4},
		{"嫩", "ㄋㄣˋ", 2e-5}, {"枘", "ㄋㄣˋ", 1e-7}, {"低", "ㄉㄧ", 2e-4}, {"刀", "ㄉㄠ", 5e-5}, {"杯", "ㄅㄟ", 5e-5}, {"拉", "ㄌㄚ", 1e-4},
		{"博", "ㄅㄛˊ", 1.4e-4}, {"播", "ㄅㄛˋ", 3e-5}, {"幹", "ㄍㄢˋ", 1.9e-4}, {"剌", "ㄌㄚˊ", 5e-7}, {"嘎", "ㄍㄚˇ", 5e-7}, {"班", "ㄅㄢ", 2e-4}, {"度", "ㄉㄨˋ", 5e-4}, {"物", "ㄨˋ", 2e-4}, {"了", "ㄌㄜ˙", 5e-3}, {"億", "ㄧˋ", 5e-4}, {"幾", "ㄐㄧˇ", 5e-4}, {"股", "ㄍㄨˇ", 3e-4},
		{"思", "ㄙ˙", 3e-6}, {"子", "ㄗ˙", 2e-4}, {"意思", "ㄧˋ ㄙ˙", 3e-4}, {"意", "ㄧˋ", 2e-4}, {"喔", "ㄛ", 9e-4}, {"呃", "ㄜ", 2e-5},
		{"阿", "ㄜ", 1e-4}, {"屋", "ㄨ", 1e-3}, {"一", "ㄧ", 2e-3}, {"淤", "ㄩ", 1e-6}, {"資", "ㄗ", 3e-4}, {"之", "ㄓ", 5e-4}, {"啊", "ㄚ", 8e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	var words []english.Word
	for _, w := range []string{"a", "hello", "win", "top", "java"} {
		words = append(words, english.Word{Text: w, Weight: 65})
	}
	s.English = english.New(words)
	return s
}

// #249：這些按鍵照字面才是使用者要的。順序顛倒（u2 是 ㄧㄉ，ps4 是 ㄣㄋ）、接在字面數字後面單一個字母（4k）或組不成音節（3nm）、
// 只有罕用字（x86 是 ㄌㄚˊ）、靠輕聲退回一聲才有字（n7）的，真的在打注音時不會這樣打。
func TestMixedAlnumTokenStaysLiteral(t *testing.T) {
	for _, keys := range []string{
		// 反序：字母接數字
		"u2", "l2", "o1", "8x", "9x", "8z", "u2 ", "l2 ", "o1 ", "8x ", "9x ", "hello u2", "hello u2 ",
		// 反序：組得成音節也不組（句首、空白、英文後），比照 #175 的 813、017
		// （#119 起數字鍵聲母晚一鍵、組成常用字的 k27 的、u24（ㄉㄧˋ）、i16 博照位置組字，見 mixed_late_initial_test.go）
		"i17", "i13", "i14", "hello i14",
		// 已知的英數詞
		"ps4", "ps3", "ji3ps4", "ji3ps3",
		// 字面數字後面
		"3x", "3nm", "7nm", "3c", "3d", "3w", "4k", "6j", "4u", "3y", "30w", "40w",
		"3x ", "3nm ", "3c ", "4k ", "3m ", "30w ",
		// 只有罕用字、靠輕聲退回一聲
		"x86", "n7",
		// 單一個大寫字母接兩個以上數字（字典認得 A）
		"A20 ", "A50 ", "A10 ",
	} {
		s := alnumTokenSession()
		mixedType(s, keys)
		if got := key(s, proto.KeyEnter).Commit; got != keys && !(len(keys) > 3 && keys[:3] == "ji3" && got == "我"+keys[3:]) {
			t.Errorf("%q 應照字面：%q", keys, got)
		}
	}
	for _, tc := range []struct{ keys, want string }{
		{"ji3x86", "我x86"}, {"ji3n7", "我n7"}, {"ji33nm", "我3nm"}, {"ji34k", "我4k"}, {"ji3u2", "我u2"}, {"ji3o1", "我o1"}, {"ji38x", "我8x"},
	} {
		s := alnumTokenSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #249：打注音的按鍵不受影響。mp3、mp4 是照順序打的允、運，語料裡中文多了幾百倍，照舊出字；緊接中文的反序（#119、#190）照舊。
func TestMixedAlnumTokenKeepsChinese(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"mp3", "允"}, {"mp4", "運"}, {"mp42j/4", "運動"}, {"mp3vm3", "允許"}, {"ji3mp4", "我運"},
		{"sp4", "嫩"}, {"ji3sp4", "我嫩"}, {"2u ", "低"}, {"2l ", "刀"}, {"1o ", "杯"}, {"x8 ", "拉"}, {"1i6", "博"}, {"e04", "幹"},
		// 緊接中文的反序（#119）
		{"ji3u2 ", "我低"}, {"ji3o1 ", "我杯"}, {"ji38x ", "我拉"}, {"ji3i16", "我博"},
		// 字面數字後面的中文
		{"3ek7", "3個"}, {"3wu0 ", "3天"}, {"7wu0 ", "7天"}, {"32k7", "3的"},
		// 字面數字後面一個字母、組成常用字
		{"4u ", "4一"}, {"6j ", "6屋"},
		// 數字接順序顛倒的注音：數字照字面，後段組字
		{"70u4", "70億"}, {"8u4", "8億"}, {"20u4", "20億"}, {"ji320u4", "我20億"}, {"30ru3", "30幾"}, {"60ej3", "60股"},
		// 照順序打的字母接數字鍵照舊
		{"e83", "嘎"}, {"e83xk7", "嘎了"},
		// 單一字母接一個數字鍵、字典認得的字接數字：中文照舊
		{"A8 ", "A啊"}, {"A5 ", "A之"}, {"hello5 ", "hello之"},
		// 數字後面取較長的音節
		{"302j4", "30度"}, {"3602j4", "360度"},
		// 數字寫法接注音（順序顛倒的組不成音節，照舊切開）
		{"3.5u4", "3.5億"}, {"7.1u4", "7.1億"}, {"4.2u4", "4.2億"}, {"20-28u4", "20-28億"},
		// 語氣詞的輕聲退回一聲照舊，詞裡的輕聲也是
		{"i7", "喔"}, {"u4n7", "意思"}, {"ji3y7", "我子"},
	} {
		s := alnumTokenSession()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #249：mp3、mp4 先出中文，字上按 ↓ 第二項就是原按鍵，選了變回英數。
func TestMixedKnownTokenKeysSecond(t *testing.T) {
	for _, tc := range []struct{ keys, shown string }{{"mp3", "允"}, {"mp4", "運"}} {
		s := alnumTokenSession()
		mixedType(s, tc.keys)
		snap := key(s, proto.KeyDown)
		got := itemTexts(snap)
		if len(got) < 2 || got[0] != tc.shown || got[1] != tc.keys {
			t.Fatalf("%q 的 ↓：%q", tc.keys, got)
		}
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[1].ID})
		if commit := key(s, proto.KeyEnter).Commit; commit != tc.keys {
			t.Errorf("%q 選原按鍵後提交 %q", tc.keys, commit)
		}
	}
	// 一般的字不受影響：原按鍵照舊排在同音字後面。
	s := alnumTokenSession()
	mixedType(s, "sp4")
	got := itemTexts(key(s, proto.KeyDown))
	if len(got) < 2 || got[1] == "sp4" {
		t.Errorf("sp4 的 ↓ 不該把原按鍵排第二：%q", got)
	}
}

// #249：照字面留下的按鍵，↓ 仍有中文可選。
func TestMixedAlnumTokenOffersChinese(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"x86", "剌"}, {"i14", "播"}, {"ps4", "嫩"},
	} {
		s := alnumTokenSession()
		mixedType(s, tc.keys)
		snap := key(s, proto.KeyDown)
		got := itemTexts(snap)
		id := slices.Index(got, tc.want)
		if len(got) < 2 || got[0] != tc.keys || id < 0 {
			t.Fatalf("%q 的 ↓：%q", tc.keys, got)
		}
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[id].ID})
		if commit := key(s, proto.KeyEnter).Commit; commit != tc.want {
			t.Errorf("%q 選 %q 後提交 %q", tc.keys, tc.want, commit)
		}
	}
}

// #249：使用者一直選回原按鍵的英數詞（個人英文已認得）下次照字面，接著打的字照常組字；別人不受影響。
func TestMixedLearnedTokenStaysLiteral(t *testing.T) {
	learned := func() *Session {
		s := alnumTokenSession()
		s.model.Store.English = map[string]personal.EnglishEvidence{
			"mp3": {Text: "mp3", Confirmed: 3, Days: 2, Updated: s.model.Store.Time, Day: s.model.Store.Time / 86400},
		}
		return s
	}
	for _, tc := range []struct{ keys, want string }{
		{"mp3", "mp3"}, {"ji3mp3", "我mp3"}, {"mp3ji3", "mp3我"}, {"mp4", "運"},
	} {
		s := learned()
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
	// 暫停學習、無痕時不用個人英文
	s := learned()
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	mixedType(s, "mp3")
	if got := key(s, proto.KeyEnter).Commit; got != "允" {
		t.Errorf("暫停學習時：%q", got)
	}
}

// #249：實際流程：選回原按鍵、隔天再選，三次之後就照字面。
func TestMixedTokenPickedBackIsLearned(t *testing.T) {
	s := alnumTokenSession()
	now := int64(1_800_000_000)
	s.model.Now = func() int64 { return now }
	for day := range 3 {
		now += 86400 * int64(day+1)
		mixedType(s, "mp3")
		snap := key(s, proto.KeyDown)
		if got := itemTexts(snap); len(got) > 1 && got[0] != "允" {
			t.Fatalf("第 %d 天還沒學會前應先出中文：%q", day, got)
		}
		s.Handle(proto.Request{Type: proto.Select, Index: snap.Candidates.Items[1].ID})
		if got := key(s, proto.KeyEnter).Commit; got != "mp3" {
			t.Fatalf("第 %d 天提交 %q", day, got)
		}
	}
	mixedType(s, "mp3")
	if got := key(s, proto.KeyEnter).Commit; got != "mp3" {
		t.Errorf("選過三次後：%q", got)
	}
}

// #247：英文模式學不到帶聲調鍵的按鍵：數字鍵會結束這個英文字，送出時算成網址、識別字的一部分不學，
// 所以忘了切回中文打的 ji3、su3 不會讓混打留成字面。只有字母的按鍵（rup）學得到，也不影響混打：聲調和空白照舊組成中文。
func TestEnglishModeDoesNotLearnToneKeys(t *testing.T) {
	s := tokenBoundarySession()
	s.EnglishAutocomplete = true
	now := int64(1_800_000_000)
	s.model.Now = func() int64 { return now }
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	for range 4 {
		now += 86400
		for _, word := range []string{"ji3", "su3", "rup"} {
			mixedType(s, word+" ")
			key(s, proto.KeyEnter)
		}
	}
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	for _, word := range []string{"ji3", "su3", "ji", "su"} {
		if _, ok := s.model.Store.English[word]; ok {
			t.Errorf("英文模式不該學到 %q：%+v", word, s.model.Store.English)
		}
	}
	if !s.model.EnglishKnown("rup") {
		t.Fatalf("測試前提：只有字母的按鍵學得到：%+v", s.model.Store.English)
	}
	for _, tc := range []struct{ keys, want string }{{"ji3", "我"}, {"rup ", "今"}} {
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// #249：個人英文不分大小寫，大寫的型號（A87）學會之後，literalToken 不能把打注音的小寫按鍵（a87 嗎）也留成字面。
// 大寫不是注音鍵，A87 本來就照字面；要小寫照字面的是自己選回原按鍵學到的（mp3，TestMixedTokenPickedBackIsLearned）。
func TestMixedLearnedCapsTokenKeepsLowercaseReading(t *testing.T) {
	s := tokenBoundarySession()
	now := int64(1_800_000_000)
	s.model.Now = func() int64 { return now }
	for range 3 {
		now += 86400
		mixedType(s, "A87 ")
		if got := key(s, proto.KeyEnter).Commit; got != "A87 " {
			t.Fatalf("A87 ：%q", got)
		}
	}
	if !s.model.EnglishKnown("A87") {
		t.Fatalf("測試前提：A87 要學到：%+v", s.model.Store.English)
	}
	for _, tc := range []struct{ keys, want string }{{"a87", "嗎"}, {"ji3a87", "我嗎"}, {"A87", "A87"}, {"A87ji3", "A87我"}} {
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q：%q，要 %q", tc.keys, got, tc.want)
		}
	}
	// 已經學到的資料也一樣：看的是學到的拼法。
	s = tokenBoundarySession()
	s.model.Store.English = map[string]personal.EnglishEvidence{
		"a87": {Text: "A87", Confirmed: 3, Days: 2, Updated: s.model.Store.Time, Day: s.model.Store.Time / 86400},
	}
	mixedType(s, "a87")
	if got := key(s, proto.KeyEnter).Commit; got != "嗎" {
		t.Errorf("a87：%q，要 嗎", got)
	}
}
