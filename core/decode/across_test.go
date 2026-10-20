package decode

import (
	"math"
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// CompleteAcross 的比例：合起來的詞群 ÷（合起來的詞群＋目前路徑在這段），更長的詞乘上 Continue（#143）。
func TestCompleteAcrossRatio(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"沒有", "ㄇㄟˊ ㄧㄡˇ", 1e-3}, {"不", "ㄅㄨˋ", 2e-3}, {"不同", "ㄅㄨˋ ㄊㄨㄥˊ", 1e-3},
		{"沒有不同", "ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ ㄊㄨㄥˊ", 2e-7}, {"沒有不透風的牆", "ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ ㄊㄡˋ ㄈㄥ ㄉㄜ˙ ㄑㄧㄤˊ", 1e-8},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		score[e.w] = e.p
	}
	d := &Decoder{Lex: lex, Score: score, Continue: 0.5}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄇㄟˊ ㄧㄡˇ ㄅㄨˋ"))
	path := []Seg{{Word: "沒有", Start: 0, End: 2}, {Word: "不", Start: 2, End: 3, Partial: true}}
	c, ratio, ok := d.CompleteAcross(lat, path, 0)
	if !ok || c.Entry.Surface != "沒有不同" {
		t.Fatalf("%v %+v", ok, c)
	}
	merged := 0.5 * (2e-7 + 1e-8)
	current := 1e-3 * (2e-3 + 0.5*1e-3)
	if want := merged / (merged + current); math.Abs(ratio-want) > 1e-12 {
		t.Errorf("比例 %g，want %g", ratio, want)
	}
	// 最後一段本身由 Complete 處理；超出範圍不回報。
	for _, from := range []int{-1, 1, 2} {
		if _, _, ok := d.CompleteAcross(lat, path, from); ok {
			t.Errorf("from=%d 不該回報", from)
		}
	}
}
