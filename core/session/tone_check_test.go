package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #300「打錯注音時提示」：按聲調（含空白的一聲）時讀音沒有字，聲調不收，注音停在暫存等修改，快照的 Bell 要外殼提示。
// 緊接著在同一處再按同一個聲調就照舊原樣留下（#109）。

func toneCheck(s *Session) *Session { s.ZhuyinErrorHint = true; return s }

func TestToneWithoutCharactersIsRefused(t *testing.T) {
	s := toneCheck(syllableLiteralSession())
	snap := keys(s, "c", "o", "6") // ㄏㄟˊ：沒有字
	if !snap.Handled || !snap.Bell || snap.Text() != "ㄏㄟ" || s.comp.String() != "ㄏㄟ" || len(s.syls) != 0 || len(s.pendingLiterals) != 0 {
		t.Fatalf("聲調不收、注音停在暫存並提示：%q 暫存 %q bell %v", snap.Text(), s.comp.String(), snap.Bell)
	}
	if len(snap.Preedit) != 1 || snap.Preedit[0].Style != proto.StylePending {
		t.Fatalf("停住的注音是 pending 那一段，外殼據此標示：%+v", snap.Preedit)
	}
	if snap = key(s, proto.KeyBackspace); snap.Bell || s.comp.String() != "ㄏ" {
		t.Fatalf("Bell 只在擋下的那次快照；退格照常刪注音：%q bell %v", s.comp.String(), snap.Bell)
	}

	// 一聲（空白）也一樣：ㄅㄩ＋空白。
	s = toneCheck(syllableLiteralSession())
	if snap = keys(s, "1", "m", " "); !snap.Bell || snap.Text() != "ㄅㄩ" || s.comp.String() != "ㄅㄩ" || len(s.literals) != 0 {
		t.Fatalf("ㄅㄩ＋空白：聲調不收、提示：%q 暫存 %q", snap.Text(), s.comp.String())
	}
	// ㄅㄩˊ：兩個符號以上、讀音沒有字照樣擋下。
	s = toneCheck(syllableLiteralSession())
	if snap = keys(s, "1", "m", "6"); !snap.Bell || snap.Text() != "ㄅㄩ" || s.comp.String() != "ㄅㄩ" || len(s.syls) != 0 {
		t.Fatalf("ㄅㄩˊ：聲調不收、提示：%q 暫存 %q", snap.Text(), s.comp.String())
	}

	// 前面已有字：停在字後面的注音。
	s = toneCheck(syllableLiteralSession())
	typeSyl(s, "ㄨㄛˇ")
	if snap = keys(s, "c", "o", "6"); !snap.Bell || snap.Text() != "我ㄏㄟ" || s.comp.String() != "ㄏㄟ" || len(s.syls) != 1 {
		t.Fatalf("我＋ㄏㄟˊ：%q 暫存 %q 音節 %v", snap.Text(), s.comp.String(), s.syls)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "我ㄏㄟ" {
		t.Fatalf("Enter 照常送出組字與未打完的注音：%q", snap.Commit)
	}
}

func TestBellReachesTheSnapshotJSON(t *testing.T) {
	s := toneCheck(syllableLiteralSession())
	b, _ := json.Marshal(keys(s, "c", "o", "6"))
	if !strings.Contains(string(b), `"bell":true`) {
		t.Fatalf("快照要帶 bell：%s", b)
	}
	b, _ = json.Marshal(key(s, proto.KeyBackspace))
	if strings.Contains(string(b), `"bell"`) {
		t.Fatalf("沒擋下時不帶 bell：%s", b)
	}
}

// 退路：緊接著在同一處再按同一個聲調，照舊原樣留下（#109），注音文多按一次仍打得出來。
func TestSameToneAgainKeepsTheLiteral(t *testing.T) {
	s := toneCheck(syllableLiteralSession())
	keys(s, "c", "o", "6")
	snap := key(s, "6")
	if snap.Bell || snap.Text() != "ㄏㄟˊ" || !s.comp.Empty() || len(s.pendingLiterals) != 1 {
		t.Fatalf("再按一次 ˊ 原樣留下 ㄏㄟˊ：%q 暫存 %q bell %v", snap.Text(), s.comp.String(), snap.Bell)
	}
	if snap = typeSyl(s, "ㄨㄛˇ"); snap.Text() != "ㄏㄟˊ我" {
		t.Fatalf("接著打的字照常（ㄏㄟˊ我）：%q", snap.Text())
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "ㄏㄟˊ我" {
		t.Fatalf("提交 %q", snap.Commit)
	}
	assertNoZhuyinLearned(t, s, "ㄏㄟˊ")

	s = toneCheck(syllableLiteralSession())
	if snap = keys(s, "1", "m", " ", " "); snap.Bell || snap.Text() != "ㄅㄩ" || !s.comp.Empty() || len(s.pendingLiterals) != 1 {
		t.Fatalf("ㄅㄩ＋空白＋空白原樣留下 ㄅㄩ：%q 暫存 %q", snap.Text(), s.comp.String())
	}
	if snap = typeSyl(s, "ㄑㄧㄢˋ"); snap.Text() != "ㄅㄩ欠" {
		t.Fatalf("ㄅㄩ 欠：%q", snap.Text())
	}

	// 別的聲調也沒有字：再擋一次，按第二次那個聲調才收下。
	s = toneCheck(syllableLiteralSession())
	keys(s, "c", "o", "6")
	if snap = key(s, "3"); !snap.Bell || s.comp.String() != "ㄏㄟ" {
		t.Fatalf("換按 ˇ 也沒有字：照樣擋下 %q bell %v", s.comp.String(), snap.Bell)
	}
	if snap = key(s, "3"); snap.Bell || snap.Text() != "ㄏㄟˇ" {
		t.Fatalf("再按一次 ˇ 收下：%q", snap.Text())
	}
}

// 「馬上」：中間有別的按鍵或請求，再按聲調仍擋下。
func TestRetryMustFollowImmediately(t *testing.T) {
	for _, between := range [][]string{
		{proto.KeyBackspace, "o"},       // 刪掉再打回同一個注音
		{proto.KeyLeft, proto.KeyRight}, // 移動游標
	} {
		s := toneCheck(syllableLiteralSession())
		typeSyl(s, "ㄨㄛˇ")
		keys(s, "c", "o", "6")
		keys(s, between...)
		if snap := key(s, "6"); !snap.Bell || s.comp.String() != "ㄏㄟ" {
			t.Errorf("%v 之後再按 ˊ 仍擋下：%q 暫存 %q", between, snap.Text(), s.comp.String())
		}
	}
	s := toneCheck(syllableLiteralSession())
	keys(s, "c", "o", "6")
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	if snap := key(s, "6"); !snap.Bell {
		t.Errorf("中間有別的請求，再按 ˊ 仍擋下：%q", snap.Text())
	}
	// 欄數不是使用者操作，不打斷退路。
	s = toneCheck(syllableLiteralSession())
	keys(s, "c", "o", "6")
	s.Handle(proto.Request{Type: proto.Display, Columns: 5})
	if snap := key(s, "6"); snap.Bell || snap.Text() != "ㄏㄟˊ" {
		t.Errorf("欄數請求不打斷退路：%q", snap.Text())
	}
}

// #85：擋下之後直接改韻母就好；用退路留下的錯音節，緊接著的同類韻母照舊改它。
func TestRefusedToneThenCorrectFinal(t *testing.T) {
	s := toneCheck(retypeSession())
	if snap := typeSyl(s, "ㄍㄨㄜˊ"); !snap.Bell || s.comp.String() != "ㄍㄨㄜ" {
		t.Fatalf("ㄍㄨㄜˊ 擋下：%q", s.comp.String())
	}
	if snap := keys(s, "i", "6"); snap.Text() != "國" {
		t.Fatalf("ㄛ 取代 ㄜ 再按 ˊ 是「國」：%q", snap.Text())
	}
	s = toneCheck(retypeSession())
	keys(s, "e", "j", "k", "6", "6")
	if snap := keys(s, "i"); snap.Text() != "ㄍㄨㄛ" || s.comp.String() != "ㄍㄨㄛ" {
		t.Fatalf("退路留下的 ㄍㄨㄜˊ 緊接 ㄛ 照舊改讀音（#85）：%q", snap.Text())
	}
	if snap := key(s, "6"); snap.Text() != "國" {
		t.Fatalf("國：%q", snap.Text())
	}
	// #85 的其他規則照舊（用退路留下錯音節之後）。
	for _, c := range []struct {
		keys []string
		want string
	}{
		{[]string{"c", "o", "6", "6", "c"}, "ㄏㄟˊㄏ"},
		{[]string{"c", "o", "6", "6", "j", "i", "3"}, "ㄏㄟˊ我"},
		{[]string{"2", " ", "u", " "}, "ㄉ一"}, // 單一符號不擋（ㄉ、ㄍˇ），不必再按一次
		{[]string{"e", "3", "i"}, "ㄍˇㄛ"},
	} {
		s := toneCheck(retypeSession())
		if snap := keys(s, c.keys...); snap.Text() != c.want {
			t.Errorf("%v：%q，想要 %q", c.keys, snap.Text(), c.want)
		}
	}
}

// 讀音有字、是詞頭（#107 的 ㄟ）或模糊音的近音有字時照常收下，不提示。
func TestToneWithReadingIsNotRefused(t *testing.T) {
	s := toneCheck(syllableLiteralSession())
	if snap := typeSyl(s, "ㄨㄛˇ"); snap.Bell || snap.Text() != "我" {
		t.Fatalf("有字的讀音照常：%q", snap.Text())
	}
	s = toneCheck(prefixOnlySession())
	if snap := keys(s, "o", " "); snap.Bell || snap.Text() != "ㄟ" || !s.comp.Empty() || len(s.literals) != 1 {
		t.Fatalf("只能當詞頭的 ㄟ 照 #107 原樣留下，不擋：%q 暫存 %q", snap.Text(), s.comp.String())
	}
	if snap := typeSyl(s, "ㄋㄧˇ"); snap.Text() != "ㄟ你" {
		t.Fatalf("ㄟ你：%q", snap.Text())
	}
	s = toneCheck(prefixOnlySession())
	typeSyl(s, "ㄋㄧ")
	if snap := typeSyl(s, "ㄗ˙"); snap.Bell || snap.Text() != "妮子" {
		t.Fatalf("詞頭接得上：%q", snap.Text())
	}

	near := toneCheck(fuzzySession(fuzzy.Config{}.With(fuzzy.SSh, fuzzy.Select), word{"送", "ㄙㄨㄥˋ", 1e-3}))
	if snap := typeSyl(near, "ㄕㄨㄥˋ"); snap.Bell || len(near.syls) != 1 || !near.comp.Empty() {
		t.Fatalf("近音 ㄙㄨㄥˋ 有字：照常收下 %q 音節 %v", snap.Text(), near.syls)
	}
	off := toneCheck(fuzzySession(fuzzy.Config{}, word{"送", "ㄙㄨㄥˋ", 1e-3}))
	if snap := typeSyl(off, "ㄕㄨㄥˋ"); !snap.Bell || off.comp.String() != "ㄕㄨㄥ" {
		t.Fatalf("模糊音關閉時 ㄕㄨㄥˋ 沒有字：擋下 %q", off.comp.String())
	}
}

// 中英混打與聲調選填不作用：照舊原樣留下，不提示。
func TestToneCheckOffInMixedInputAndToneless(t *testing.T) {
	for _, k := range [][]string{{"c", "o", "6"}, {"1", " "}} {
		plain := syllableLiteralSession()
		plain.MixedInput = true
		want := keys(plain, k...).Text()
		s := toneCheck(syllableLiteralSession())
		s.MixedInput = true
		if snap := keys(s, k...); snap.Bell || snap.Text() != want {
			t.Errorf("中英混打 %v 不擋，照舊 %q：%q", k, want, snap.Text())
		}
	}

	plain := syllableLiteralSession()
	plain.Toneless = true
	want := keys(plain, "c", "o", "6").Text()
	s := toneCheck(syllableLiteralSession())
	s.Toneless = true
	if snap := keys(s, "c", "o", "6"); snap.Bell || snap.Text() != want {
		t.Fatalf("聲調選填不擋，照舊 %q：%q", want, snap.Text())
	}
	// 設定關閉（零值）時同沒有這個功能。
	if snap := keys(syllableLiteralSession(), "c", "o", "6"); snap.Bell || snap.Text() != "ㄏㄟˊ" {
		t.Fatalf("關閉時照舊原樣留下：%q", snap.Text())
	}
}

// 許氏與倚天：聲調鍵依排列解讀，擋下與退路都看聲調，不看按鍵。
func TestToneCheckWithOtherLayouts(t *testing.T) {
	s := toneCheck(hsuSession(t))
	hk := layout.Hsu.Keys(zhuyin.Syllable("ㄏㄟˊ"))
	snap := typeKeys(s, hk)
	if !snap.Bell || s.comp.String() != "ㄏㄟ" {
		t.Fatalf("許氏 %q：擋下 %q", hk, s.comp.String())
	}
	if snap = typeKeys(s, hk[len(hk)-1:]); snap.Bell || snap.Text() != "ㄏㄟˊ" {
		t.Fatalf("許氏再按一次 %q 收下：%q", hk[len(hk)-1:], snap.Text())
	}
	if snap = typeKeys(toneCheck(hsuSession(t)), "hnf"); snap.Bell || snap.Text() != "很" {
		t.Fatalf("許氏有字照常：%q", snap.Text())
	}
	// 許氏只打 ㄇ 按聲調是 ㄢ（收尾修正）：ㄢˊ 沒有字，但只有一個符號，不擋，照舊原樣留下 ㄢˊ。
	s = toneCheck(hsuSession(t))
	if snap = typeKeys(s, "md"); snap.Bell || snap.Text() != "ㄢˊ" || !s.comp.Empty() {
		t.Fatalf("許氏 ㄇ＋d 原樣留下 ㄢˊ：%q 暫存 %q", snap.Text(), s.comp.String())
	}
	if snap = typeKeys(toneCheck(hsuSession(t)), "m "); snap.Bell || snap.Text() != "安" {
		t.Fatalf("許氏 ㄇ＋空白 是「安」：%q", snap.Text())
	}

	s = toneCheck(etenSession(t))
	ek := layout.ETen.Keys(zhuyin.Syllable("ㄅㄧㄣˊ"))
	if snap = typeKeys(s, ek); !snap.Bell || s.comp.String() != "ㄅㄧㄣ" {
		t.Fatalf("倚天 %q：擋下 %q", ek, s.comp.String())
	}
	if snap = typeKeys(s, ek[len(ek)-1:]); snap.Bell || snap.Text() != "ㄅㄧㄣˊ" {
		t.Fatalf("倚天再按一次收下：%q", snap.Text())
	}
	if snap = typeETen(toneCheck(etenSession(t)), "ㄑㄩˋ"); snap.Bell || snap.Text() != "去" {
		t.Fatalf("倚天有字照常：%q", snap.Text())
	}
}

// 只有一個符號的注音（ㄟ、ㄏ＋聲調、ㄅ＋空白）不擋，照舊原樣留下（#109）：聊天常打的「ㄟ你在哪裡」，
// 擋下的話接著打的注音全疊到 ㄟ 上（ㄋㄧㄟ…最後成了「倆」）。
func TestSingleSymbolIsNotRefused(t *testing.T) {
	s := toneCheck(syllableLiteralSession(
		word{"你", "ㄋㄧˇ", 5e-3}, word{"在", "ㄗㄞˋ", 5e-3}, word{"哪裡", "ㄋㄚˇ ㄌㄧˇ", 1e-3}, word{"倆", "ㄌㄧㄚˇ", 1e-4},
	))
	snap := keys(s, "o", " ")
	if snap.Bell || snap.Text() != "ㄟ" || !s.comp.Empty() || len(s.literals) != 1 {
		t.Fatalf("句首 ㄟ＋空白原樣留下、不提示：%q 暫存 %q bell %v", snap.Text(), s.comp.String(), snap.Bell)
	}
	for _, syl := range []string{"ㄋㄧˇ", "ㄗㄞˋ", "ㄋㄚˇ", "ㄌㄧˇ"} {
		if snap = typeSyl(s, syl); snap.Bell {
			t.Fatalf("%s 不提示：%q", syl, snap.Text())
		}
	}
	if snap.Text() != "ㄟ你在哪裡" {
		t.Fatalf("ㄟ你在哪裡：%q", snap.Text())
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "ㄟ你在哪裡" {
		t.Fatalf("提交 %q", snap.Commit)
	}

	for _, c := range []struct {
		keys []string
		want string
	}{
		{[]string{"c", "6"}, "ㄏˊ"},
		{[]string{"c", "3"}, "ㄏˇ"},
		{[]string{"c", "4"}, "ㄏˋ"},
		{[]string{"c", "7"}, "ㄏ˙"},
		{[]string{"1", " "}, "ㄅ"},
		{[]string{"o", "6"}, "ㄟˊ"},
	} {
		s := toneCheck(syllableLiteralSession())
		snap := keys(s, c.keys...)
		if snap.Bell || snap.Text() != c.want || !s.comp.Empty() || len(s.pendingLiterals)+len(s.literals) == 0 {
			t.Errorf("%v 原樣留下 %q、不提示：%q 暫存 %q bell %v", c.keys, c.want, snap.Text(), s.comp.String(), snap.Bell)
		}
	}

	// 前面已有字也一樣：我＋ㄅ＋空白＋ㄑㄧㄢˋ。
	s = toneCheck(syllableLiteralSession())
	typeSyl(s, "ㄨㄛˇ")
	if snap = keys(s, "1", " "); snap.Bell || snap.Text() != "我ㄅ" || !s.comp.Empty() {
		t.Fatalf("我＋ㄅ＋空白：%q 暫存 %q", snap.Text(), s.comp.String())
	}
	if snap = typeSyl(s, "ㄑㄧㄢˋ"); snap.Text() != "我ㄅ欠" {
		t.Fatalf("我ㄅ欠：%q", snap.Text())
	}
}
