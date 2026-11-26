package rerank

import (
	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"math"
	"strings"
	"testing"
)

func fixture() Request {
	return Request{Reading: zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), Candidates: []decode.Hypothesis{
		{Text: "甲乙", LogScore: -2, Segments: []decode.Seg{{Word: "甲乙", Start: 0, End: 2}}},
		{Text: "甲已", LogScore: -3, Segments: []decode.Seg{{Word: "甲已", Start: 0, End: 2}}},
	}}
}
func TestChooseStableFiniteAndMargin(t *testing.T) {
	r := fixture()
	if !Supported(r) {
		t.Fatal("fixture unsupported")
	}
	for _, c := range []struct {
		s    []float64
		w, m float64
		want int
	}{
		{[]float64{0, 10}, .1, 0, 0}, {[]float64{0, 11}, .1, 0, 1}, {[]float64{0, 11}, .1, .2, 0},
		{[]float64{math.NaN(), 11}, .1, 0, 0}, {[]float64{0, math.Inf(1)}, .1, 0, 0},
		{nil, .1, 0, 0}, {[]float64{0, 11}, 0, 0, 0}, {[]float64{0, 11}, .1, -1, 0},
	} {
		if got := Choose(r.Candidates, c.s, c.w, c.m); got != c.want {
			t.Fatalf("%+v got %d", c, got)
		}
	}
}
func TestUnsupportedCasesRemainWhole(t *testing.T) {
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Candidates[1].Segments[0].Partial = true },
		func(r *Request) { r.Candidates[1].Segments[0].Correction = true },
		func(r *Request) { r.Candidates[1].Segments[0].Start = 1 },
		func(r *Request) { r.Candidates[1].Text = "甲" },
		func(r *Request) { r.Reading[0] = "ma" },
		func(r *Request) { r.Candidates[1] = r.Candidates[0] },
		func(r *Request) { r.Context = strings.Repeat("字", MaxContextRunes+1) },
		func(r *Request) { r.Context = "\xff" },
	} {
		r := fixture()
		mutate(&r)
		if Supported(r) {
			t.Fatalf("malformed case supported: %+v", r)
		}
	}
}

func TestContextWithinLimitIsSupported(t *testing.T) {
	r := fixture()
	r.Context = strings.Repeat("字", MaxContextRunes)
	if !Supported(r) {
		t.Fatal("64-rune confirmed context rejected")
	}
}

func TestAlignedPartialIsExplicitAndNeverAddsFutureText(t *testing.T) {
	r := fixture()
	r.Candidates[1].Segments[0].Partial = true
	if Supported(r) {
		t.Fatal("default accepted Partial")
	}
	r.AllowPartial = true
	if !Supported(r) {
		t.Fatal("equal-length Partial rejected")
	}
	r.Candidates[1].Segments[0].Word = "甲已丙"
	if Supported(r) {
		t.Fatal("untyped suffix accepted")
	}
	r = fixture()
	r.AllowPartial = true
	r.Candidates[1].Segments[0].Correction = true
	if Supported(r) {
		t.Fatal("Correction silently enabled")
	}
}

func TestTextModeNeedsSingleSyllableAndContext(t *testing.T) {
	r := Request{Mode: ModeText, Context: "失敗", Reading: zhuyin.ParseReading("ㄇㄚ"), Candidates: []decode.Hypothesis{
		{Text: "媽", LogScore: -2, Segments: []decode.Seg{{Word: "媽", Start: 0, End: 1}}},
		{Text: "嗎", LogScore: -3, Segments: []decode.Seg{{Word: "嗎", Start: 0, End: 1}}},
	}}
	if !Supported(r) {
		t.Fatal("single-syllable text request rejected")
	}
	for _, bad := range []func(*Request){
		func(r *Request) { r.Context = "" },
		func(r *Request) { r.Mode = "" },
		func(r *Request) { r.Mode = "other" },
	} {
		c := r
		bad(&c)
		if Supported(c) {
			t.Fatalf("unsupported request accepted: %+v", c)
		}
	}
	if f := fixture(); func() bool { f.Mode, f.Context = ModeText, "前文"; return Supported(f) }() {
		t.Fatal("text mode must stay single-syllable")
	}
}

// 多字的文字評分（九宮格挑拼法）只送給宣告 TextAnyLength 的評分器；前文可以是空的。單音節的文字評分不論宣告與否
// 都要有前文，讀音評分的條件不變。
func TestTextAnyLengthIsDeclared(t *testing.T) {
	text := func(context string, n int) Request {
		r := fixture()
		r.Mode, r.Context = ModeText, context
		r.Reading = zhuyin.ParseReading(strings.TrimSpace(strings.Repeat("ㄐㄧㄚˇ ", n)))
		for i, ch := range []string{"甲", "假"} {
			w := strings.Repeat(ch, n)
			r.Candidates[i].Text, r.Candidates[i].Segments = w, []decode.Seg{{Word: w, Start: 0, End: n}}
		}
		return r
	}
	off, on := Options{}, Options{TextAnyLength: true}
	for _, c := range []struct {
		r       Request
		off, on bool
	}{
		{fixture(), true, true},
		{text("", 2), false, true},
		{text("前文", 2), false, true},
		{text("", 32), false, true},
		{text("", 33), false, false},
		{text("前文", 1), true, true},
		{text("", 1), false, false},
	} {
		if Supported(c.r) != c.off || off.Supports(c.r) != c.off || on.Supports(c.r) != c.on {
			t.Fatalf("%d syllables, context %q: Supported %v, off %v, on %v; want off %v on %v",
				len(c.r.Reading), c.r.Context, Supported(c.r), off.Supports(c.r), on.Supports(c.r), c.off, c.on)
		}
	}
}
