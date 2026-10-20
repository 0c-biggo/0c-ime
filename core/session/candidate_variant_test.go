package session

import (
	"slices"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// #171：打「空氣」後看單字候選，日文新字體「気」排在「氣」前面。維基的日文內容讓「空→気」有 bigram，
// 「空→氣」卻因為「空氣」本身是詞而沒有單獨的配對，單字候選以前後文評分時，詞頻低幾百倍的気反而贏。
// 編譯器把只來自全字庫的字標為罕用（有詞頻也一樣），候選視窗排在常用字後面；仍打得出來。
func kongqiSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		word, reading string
		p             float64
		rare          bool
	}{
		{"空氣", "ㄎㄨㄥ ㄑㄧˋ", 1e-4, false},
		{"空", "ㄎㄨㄥ", 1e-4, false},
		{"氣", "ㄑㄧˋ", 6.4e-5, false},
		{"器", "ㄑㄧˋ", 4.8e-5, false},
		{"企", "ㄑㄧˋ", 3e-5, false},
		{"気", "ㄑㄧˋ", 1.5e-7, true},
		{"炁", "ㄑㄧˋ", 1e-9, true},
	} {
		lex.Add(lexicon.Entry{Surface: e.word, Reading: zhuyin.ParseReading(e.reading), PRW: 1, Rare: e.rare})
		sys.SetUni(e.word, e.p)
	}
	// 0.2.0 出貨字典的實際值：P(気|空) 2.1e-4、P(器|空) 2.5e-4，P(氣|空) 沒有配對。
	sys.SetBi("空", "気", 2.1e-4)
	sys.SetBi("空", "器", 2.5e-4)
	return New(personal.NewModel(lex, sys))
}

func TestCommonCharacterBeforeJapaneseVariantInSingles(t *testing.T) {
	s := kongqiSession()
	typeSyl(s, "ㄎㄨㄥ")
	typeSyl(s, "ㄑㄧˋ")
	if got := s.snapshot().Text(); got != "空氣" {
		t.Fatalf("fixture decodes %q", got)
	}
	key(s, proto.KeyDown)
	items := texts(allItems(s))
	singles := items[s.cand.split:]
	qi, ki := slices.Index(singles, "氣"), slices.Index(singles, "気")
	if qi < 0 || ki < 0 || qi > ki {
		t.Fatalf("氣 must come before 気 in the singles: %v", items)
	}
	// 罕用字之間照分數排，仍在清單裡（打得出來）。
	if ri := slices.Index(singles, "炁"); ri < ki {
		t.Fatalf("rare characters keep their score order: %v", items)
	}
	// 單字依選了之後的整句排序（#172）：選「氣」仍組回「空氣」，所以「氣」排在有「空→器」配對的「器」前面。
	if qi != 0 || slices.Index(singles, "器") < 0 {
		t.Fatalf("氣 (part of 空氣) must lead the singles and 器 must stay: %v", items)
	}
}
