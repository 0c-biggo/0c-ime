package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// englishContextSession 模擬真實資料包：句首的「以下」比「一下」常見（新聞的「以下是」），單看頻率是「一下」。
func englishContextSession(context bool) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"以下", "ㄧˇ ㄒㄧㄚˋ", 1e-4}, {"一下", "ㄧˊ ㄒㄧㄚˋ", 3e-4}, {"我", "ㄨㄛˇ", 1e-2},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi(lm.BOS, "以下", 0.05)
	sys.SetBi(lm.BOS, "一下", 1e-3)
	s := New(personal.NewModel(lex, sys))
	s.Toneless, s.Abbreviation, s.MixedWithToneless, s.MixedInput = true, true, true, true
	s.EnglishContext = context
	return s
}

// 點進組字的英文字後面，中文照句子中間解（APP#33）：check一下，不是句首的「以下」。沒打開時照舊從句首算。
func TestEnglishContextAfterAWord(t *testing.T) {
	for _, c := range []struct {
		context bool
		before  string // 換成英文字的鍵（check 的字母位置：ㄏ ㄘ ㄍ 收完，ㄏㄜ 未打完）；空的是沒有英文字
		want    string
	}{
		{true, "ㄏㄘㄍㄏㄜ", "check一下"},
		{false, "ㄏㄘㄍㄏㄜ", "check以下"},
		{true, "", "以下"},
	} {
		s := englishContextSession(c.context)
		word := ""
		if c.before != "" {
			typeSymbols(s, c.before)
			word = "check"
			s.Handle(proto.Request{Type: proto.Literal, Length: 3, Word: word})
		}
		typeSymbols(s, "ㄧㄒㄧㄚ")
		if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != c.want {
			t.Errorf("context=%v %q：送出 %q，要 %q", c.context, word, got, c.want)
		}
	}
}

// 英文詞的範圍：中間一個半形空白還算；前面緊接數字的型號（A4）、標點與中文都不算。
func TestEnglishContextLeft(t *testing.T) {
	for _, c := range []struct {
		text []string // 字面文字（中文字以 "" 代表一個打的音節）
		want string
	}{
		{[]string{"c", "h", "e", "c", "k"}, "check"},
		{[]string{"c", "h", "e", "c", "k", " "}, "check"},
		{[]string{"I", "'", "m"}, "I'm"},
		{[]string{"A", "4"}, ""},
		{[]string{"4", "A"}, ""},
		{[]string{"，"}, ""},
		{[]string{"o", "k", "，"}, ""},
		{[]string{"", "a", "p", "p"}, "app"},
		{[]string{"a", "p", "p", " ", " "}, ""},
	} {
		s := englishContextSession(true)
		s.literals = map[int]bool{}
		for i, t := range c.text {
			if t == "" {
				s.syls = append(s.syls, "ㄨㄛˇ")
				continue
			}
			s.syls = append(s.syls, zhuyin.Syllable(t))
			s.literals[i] = true
		}
		if got := s.englishLeft(len(s.syls)); got != c.want {
			t.Errorf("%q：%q，要 %q", c.text, got, c.want)
		}
	}
}
