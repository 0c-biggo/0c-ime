package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #175（方案 b）用的小詞庫：ㄓ˙、ㄅㄢ˙ 沒有字，要靠 #69 的輕聲退回一聲才組得成。
func digitToneSession(mixed bool) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"我", "ㄨㄛˇ", 1e-2}, {"是", "ㄕˋ", 5e-3}, {"歲", "ㄙㄨㄟˋ", 3e-4},
		{"之", "ㄓ", 1e-3}, {"只", "ㄓˇ", 2e-3}, {"至", "ㄓˋ", 1e-3}, {"只是", "ㄓˇ ㄕˋ", 1e-3},
		{"班", "ㄅㄢ", 3e-4}, {"把", "ㄅㄚˇ", 2e-3}, {"啊", "ㄚ˙", 1e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = mixed
	return s
}

func TestMixedDigitToneStaysNumber(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"57", "57"},       // ㄓ˙ 沒有字，不退回一聲的「之」
		{"57 ", "57 "},     // 接空白是數字
		{"ji357", "我57"},   // 接在中文後面也一樣
		{"57njo4", "57歲"},  // 後面直接接中文
		{"107", "107"},     // ㄅㄢ˙ 沒有字
		{"354", "354"},     // 54 緊接在字面的 3 後面，是數字的一部分
		{"813", "813"},     // ㄚㄅ 順序顛倒
		{"53", "只"},        // 有字的照舊出字
		{"54", "至"},        //
		{"87", "啊"},        // ㄚ˙ 有字
		{"183", "把"},       //
		{"ji353g4", "我只是"}, //
	} {
		s := digitToneSession(true)
		if snap := mixedType(s, tc.keys); snap.Text() != tc.want {
			t.Errorf("%q 顯示 %q，要 %q", tc.keys, snap.Text(), tc.want)
		}
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
}

// 沒開混打時 #69 照舊：ㄓ˙ 退回一聲出「之」。
func TestDigitToneNeutralFallbackWithoutMixed(t *testing.T) {
	s := digitToneSession(false)
	for _, k := range []string{"5", "7"} {
		key(s, k)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "之" {
		t.Fatalf("混打關閉時 5 7 提交 %q，要「之」", got)
	}
}

// 數字＋聲調鍵出的字，↓ 的第二個候選是原按鍵，選了變回數字。
func TestMixedDigitToneDownOffersNumberSecond(t *testing.T) {
	for _, tc := range []struct{ keys, first, number string }{
		{"54", "至", "54"},
		{"53", "只", "53"},
	} {
		s := digitToneSession(true)
		mixedType(s, tc.keys)
		items := downItems(key(s, proto.KeyDown))
		if len(items) < 2 || items[0] != tc.first || items[1] != tc.number {
			t.Fatalf("%q 按 ↓：%q", tc.keys, items)
		}
		if snap := s.Handle(proto.Request{Type: proto.Select, Index: 1}); snap.Text() != tc.number {
			t.Fatalf("%q 選回數字：%q", tc.keys, snap.Text())
		}
	}
}
