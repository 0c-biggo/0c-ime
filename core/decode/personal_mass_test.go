package decode

import (
	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
	"math"
	"testing"
)

type massScore struct{ system, personal map[string]float64 }

func (s *massScore) Prob(_ string, w string) float64       { return s.personal[w] }
func (s *massScore) SystemProb(_ string, w string) float64 { return s.system[w] }
func (s *massScore) PriorMass(_ string, w string) float64  { return s.system[w] }
func (s *massScore) HasAdaptation() bool                   { return true }

func TestOrdinaryAdaptationConservesReadingMass(t *testing.T) {
	lex := lexicon.NewMem()
	r := zhuyin.ParseReading("ㄐㄧㄚˇ")
	entries := []lexicon.Entry{{Surface: "甲", Reading: r, PRW: 1}, {Surface: "假", Reading: r, PRW: .1}}
	for _, e := range entries {
		lex.Add(e)
	}
	score := &massScore{map[string]float64{"甲": .01, "假": .02}, map[string]float64{"甲": .001, "假": .9}}
	d := Decoder{Lex: lex, Score: score}
	total := 0.
	for _, e := range entries {
		total += d.languageProbability("", e) * e.PRW
	}
	if math.Abs(total-.012) > 1e-12 {
		t.Fatalf("ordinary evidence inflated reading mass: %g", total)
	}
	if d.languageProbability("", entries[1])*.1 <= d.languageProbability("", entries[0]) {
		t.Fatal("preference within homophones was lost")
	}
	// Multiple sources for the same surface cannot multiply its mass.
	lex.Add(entries[0])
	d.massScale = nil
	duplicate := 0.
	for _, e := range entries {
		duplicate += d.languageProbability("", e) * e.PRW
	}
	if math.Abs(duplicate-total) > 1e-12 {
		t.Fatalf("duplicates changed mass: %g vs %g", duplicate, total)
	}
}

func TestOrdinaryAdaptationDoesNotRewardSplitting(t *testing.T) {
	lex := lexicon.NewMem()
	for _, x := range []struct{ w, r string }{{"甲", "ㄐㄧㄚˇ"}, {"假", "ㄐㄧㄚˇ"}, {"乙", "ㄧˇ"}, {"甲乙", "ㄐㄧㄚˇ ㄧˇ"}} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
	}
	score := &massScore{map[string]float64{"甲": .001, "假": .001, "乙": .5, "甲乙": .005}, map[string]float64{"甲": .8, "假": .001, "乙": .5, "甲乙": .005}}
	d := Decoder{Lex: lex, Score: score}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"))
	path := d.Decode(lat, nil, false)
	if len(path) != 1 || path[0].Word != "甲乙" {
		t.Fatalf("single-word evidence forced a split: %+v", path)
	}
	// A new request must see changes to counts, not the previous normalization.
	score.personal["甲"] = .001
	score.personal["假"] = .8
	path = d.Decode(lat[:1], nil, false)
	if len(path) != 1 || path[0].Word != "假" {
		t.Fatal("new counts were hidden by cached normalization", path)
	}
	x := d.ExplainBest(lat, nil, false)
	sum := 0.
	for _, s := range x.Segments {
		sum += s.LogScore
		for _, c := range s.Contributions {
			want := c.LanguageProbability * c.ReadingWeight * c.FrequencyScale * math.Exp(-c.InputCost) * c.Continuation
			if math.Abs(want-c.Mass) > 1e-12 {
				t.Fatal("explanation lost frequency normalization")
			}
		}
	}
	if math.Abs(sum-x.LogScore) > 1e-12 {
		t.Fatal("explanation disagrees with search")
	}
}

func TestPrefixGuardNeverRaisesUnobservedMass(t *testing.T) {
	lex := lexicon.NewMem()
	e := lexicon.Entry{Surface: "甲", Reading: zhuyin.ParseReading("ㄐㄧㄚˇ"), PRW: 1}
	lex.Add(e)
	score := &massScore{map[string]float64{"甲": .1}, map[string]float64{"甲": .001}}
	d := Decoder{Lex: lex, Score: score}
	if got := d.languageProbability("", e); got != .001 {
		t.Fatalf("normalization itself boosted an unobserved fragment: %g", got)
	}
}
