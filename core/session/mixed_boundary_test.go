package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func mixedTyped(seq string) (*Session, proto.Snapshot) {
	s := newSession()
	s.MixedInput = true
	var snap proto.Snapshot
	for _, r := range seq {
		snap = key(s, string(r))
	}
	return s, snap
}

// 數字或標點後面直接接注音：2024ji3 → 2024我，,ji3 → ,我。
func TestMixedDigitsOrPunctuationBeforeZhuyin(t *testing.T) {
	for seq, want := range map[string]string{
		"2024ji3":   "2024我",
		"ji3,ji3":   "我,我",
		"hello,ji3": "hello,我",
	} {
		s, _ := mixedTyped(seq)
		if got := key(s, proto.KeyEnter).Commit; got != want {
			t.Fatalf("%s：%q，要 %q", seq, got, want)
		}
	}
}

// 純數字不拆：154 不是「1這」，15 不是「1之」。
func TestMixedPlainNumbersStayDigits(t *testing.T) {
	for seq, want := range map[string]string{"154": "154", "hello 15 ": "hello 15 ", "ji3 2 ": "我 2 ", "10:30 ": "10:30 ", "18": "18"} {
		s, _ := mixedTyped(seq)
		if got := key(s, proto.KeyEnter).Commit; got != want {
			t.Fatalf("%s：%q，要 %q", seq, got, want)
		}
	}
}

// 中文後面的單獨標點鍵照字面：「我.」不是「我ㄡ」；接聲調仍是注音。
func TestMixedTrailingPunctuationKeyIsLiteral(t *testing.T) {
	s, snap := mixedTyped("ji3.")
	if snap.Text() != "我." {
		t.Fatalf("顯示：%q", snap.Text())
	}
	if got := key(s, proto.KeyEnter).Commit; got != "我." {
		t.Fatalf("提交：%q", got)
	}
}

func numberSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct{ w, r string }{{"我", "ㄨㄛˇ"}, {"八", "ㄅㄚ"}, {"單", "ㄉㄢ"}, {"班", "ㄅㄢ"}, {"暈", "ㄩㄣ"}, {"詩", "ㄕ"}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, 1e-3)
	}
	s := New(personal.NewModel(lex, sys))
	s.MixedInput = true
	return s
}

// code review：只有數字的待定按鍵接空白，前面是中文才當一聲；開頭或接在空白、英文後是數字。
// #162 起只用數字鍵拼得成一聲字的（20 單、18 八）不論位置都是中文，拼不成的（15、12）照舊是數字。
// 數字＋英文縮寫（5pm、5g、1080p）接空白不拆成數字＋中文。
func TestMixedNumbersBeforeSpaceFollowContext(t *testing.T) {
	for seq, want := range map[string]string{
		"20 ":     "單",
		"ji3 20 ": "我 單",
		"15 ":     "15 ",
		"ji3 15 ": "我 15 ",
		"ji318 ":  "我八",
		"ji310 ":  "我班",
	} {
		s := numberSession()
		for _, r := range seq {
			key(s, string(r))
		}
		if got := key(s, proto.KeyEnter).Commit; got != want {
			t.Fatalf("%q：%q，要 %q", seq, got, want)
		}
	}
	for _, seq := range []string{"5pm ", "5g ", "1080p "} {
		s := numberSession()
		for _, r := range seq {
			key(s, string(r))
		}
		if got := key(s, proto.KeyEnter).Commit; got != seq {
			t.Fatalf("%q 不能拆成數字＋中文或注音符號：%q", seq, got)
		}
	}
}
