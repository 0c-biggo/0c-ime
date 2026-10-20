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
