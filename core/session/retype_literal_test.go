package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #85：打錯注音、按下聲調才發現沒有字（ㄍㄨㄜˊ）時，音節原樣留下（#109）；緊接著打的同類
// 介音／韻母是在改這個讀音，不是新的音節：ㄛ 取代 ㄜ、音節回到注音暫存等聲調，和按聲調前
// 直接打 ㄛ 一樣。

func retypeSession() *Session {
	return syllableLiteralSession(
		word{"國", "ㄍㄨㄛˊ", 2e-3}, word{"果", "ㄍㄨㄛˇ", 1e-3}, word{"格", "ㄍㄜˊ", 1e-3},
		word{"一", "ㄧ", 5e-3}, word{"低", "ㄉㄧ", 1e-3}, word{"哈", "ㄏㄚ", 1e-3},
	)
}

func TestNextFinalCorrectsUntypableSyllable(t *testing.T) {
	s := retypeSession()
	if snap := typeSyl(s, "ㄍㄨㄜˊ"); snap.Text() != "ㄍㄨㄜˊ" || !s.comp.Empty() {
		t.Fatalf("沒有字的音節照舊原樣留下（#109）：%q 暫存 %q", snap.Text(), s.comp.String())
	}
	snap := key(s, "i") // ㄛ
	if snap.Text() != "ㄍㄨㄛ" || s.comp.String() != "ㄍㄨㄛ" || len(s.syls) != 0 || len(s.pendingLiterals) != 0 {
		t.Fatalf("ㄛ 取代 ㄜ，音節回到暫存等聲調，不是接在後面變成 ㄍㄨㄜˊㄛ：%q 暫存 %q 音節 %v", snap.Text(), s.comp.String(), s.syls)
	}
	if snap = key(s, "6"); snap.Text() != "國" {
		t.Fatalf("改好後照常按聲調成字：%q", snap.Text())
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "國" {
		t.Fatalf("提交 %q", snap.Commit)
	}
}

func TestCorrectedSyllableTakesAnyTone(t *testing.T) {
	s := retypeSession()
	if snap := keys(s, "e", "j", "k", "6", "i", "3"); snap.Text() != "果" {
		t.Fatalf("改好後可以按別的聲調（ㄍㄨㄛˇ）：%q", snap.Text())
	}
	s = retypeSession()
	if snap := keys(s, "e", "i", "6", "k", "6"); snap.Text() != "格" {
		t.Fatalf("ㄍㄛˊ 再打 ㄜ 改成 ㄍㄜ，按 ˊ 得「格」：%q", snap.Text())
	}
	s = retypeSession()
	if snap := keys(s, "e", "j", "k", " ", "i", "6"); snap.Text() != "國" {
		t.Fatalf("空白（一聲）完成的錯音節同樣可改：%q", snap.Text())
	}
	s = retypeSession()
	keys(s, "e", "j", "k", "6", "i")
	if snap := key(s, proto.KeyBackspace); s.comp.String() != "ㄍㄨ" || snap.Text() != "ㄍㄨ" {
		t.Fatalf("改好後退格刪最後一個注音：%q 暫存 %q", snap.Text(), s.comp.String())
	}
}

// 改讀音只限緊接著的一鍵，而且必須取代同類的介音／韻母、改完的讀音有字；其他情形照舊從新的音節
// 開始，刻意留下的原樣注音（ㄏㄟˊ、ㄅ、ㄉ）不會被後面的字吃掉。
func TestOtherKeysStillStartANewSyllable(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		want string
	}{
		{"聲母不取代（ㄏㄟˊ 後接 ㄏ）", []string{"c", "o", "6", "c"}, "ㄏㄟˊㄏ"},
		{"補上原本沒有的介音不算改（ㄏㄟˊ 我）", []string{"c", "o", "6", "j", "i", "3"}, "ㄏㄟˊ我"},
		{"單打聲母後的韻母是新音節（ㄉ 一）", []string{"2", " ", "u", " "}, "ㄉ一"},
		{"ㄍˇ 後的 ㄛ 不是取代", []string{"e", "3", "i"}, "ㄍˇㄛ"},
		{"改完仍沒有字就不改（ㄍㄧㄜ）", []string{"e", "j", "k", "6", "u"}, "ㄍㄨㄜˊㄧ"},
		{"同一個韻母再打一次不是改", []string{"e", "j", "k", "6", "k"}, "ㄍㄨㄜˊㄜ"},
		{"中間移過游標就不是緊接著", []string{"e", "j", "k", "6", proto.KeyLeft, proto.KeyRight, "i"}, "ㄍㄨㄜˊㄛ"},
		{"ㄅ 欠", []string{"1", " ", "f", "u", "0", "4"}, "ㄅ欠"},
	}
	for _, c := range cases {
		s := retypeSession()
		if snap := keys(s, c.keys...); snap.Text() != c.want {
			t.Errorf("%s：%q，想要 %q", c.name, snap.Text(), c.want)
		}
	}
}

// 從 ↓ 明確選入的原樣音節是使用者要的字，不是打錯，不會被下一鍵改掉。
func TestChosenSyllableLiteralIsNotRetyped(t *testing.T) {
	s := retypeSession()
	keys(s, "c", "o", "6")
	key(s, proto.KeyDown)
	key(s, proto.KeyEnter)
	if snap := key(s, "8"); snap.Text() != "ㄏㄟˊㄚ" {
		t.Fatalf("選入的原樣音節後的 ㄚ 是新音節：%q", snap.Text())
	}
}
