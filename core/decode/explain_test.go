package decode

import (
	"math"
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestSeparateContinuationDoesNotMarkCompleteWordPartial(t *testing.T) {
	d := fixture()
	d.SeparateContinuation = true
	d.Continue = .5
	d.Score.(uni)["因為"] = 1e-5
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄣ"))
	p := d.Decode(lat, nil, true)
	if len(p) != 1 || p[0].Partial || p[0].Word != "因" {
		t.Fatal(p)
	}
	// 提高長詞支持後仍可做前綴預測。
	d.Score.(uni)["因為"] = .1
	p = d.Decode(lat, nil, true)
	if len(p) != 1 || !p[0].Partial {
		t.Fatal(p)
	}
}

func TestPrefixMassDoesNotCountSameWordTwice(t *testing.T) {
	d := fixture()
	d.SeparateContinuation = true
	reading := zhuyin.ParseReading("ㄧㄣ ㄨㄟˋ")
	e := lexicon.Entry{Surface: "因為", Reading: reading, PRW: 1}
	a := d.group([]tailEntry{{e, 0}}, 1, "<s>", .5)
	b := d.group([]tailEntry{{e, 0}, {e, 1}, {e, 0}}, 1, "<s>", .5)
	if len(a) != 1 || len(b) != 1 || a[0].mass != b[0].mass {
		t.Fatal(a, b)
	}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄣ"))
	path := []Seg{{Word: "因", Start: 0, End: 1}}
	before, ok := d.Complete(lat, path)
	if !ok {
		t.Fatal("需要可比較的補全")
	}
	d.Lex.(*lexicon.Mem).Add(e)
	d.cache = nil
	after, ok := d.Complete(lat, path)
	if !ok || before.Entry.Surface != after.Entry.Surface || math.Abs(before.Conf-after.Conf) > 1e-12 {
		t.Fatal("重複讀音不能改變補全信心", before, after)
	}
}

func TestExplainedScoresAndBiasedCandidatesMatchSearch(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		for _, separate := range []bool{false, true} {
			d := fixture()
			d.SeparateContinuation = separate
			d.Continue = .5
			d.CorrectionPenalty = func(start, end int, w string) float64 {
				if w == "因" {
					return 2
				}
				return 0
			}
			d.Evidence = func(start, end int, left, w string) (float64, float64) {
				if w == "因" {
					return 1, 1
				}
				return 0, 1
			}
			lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄣ"))
			x := d.ExplainBest(lat, nil, prefix)
			sum := 0.0
			for _, g := range x.Segments {
				sum += g.LogScore
				mass := 0.0
				for _, c := range g.Contributions {
					mass += c.Mass
				}
				if math.Abs(math.Log(mass)-g.CorrectionPenalty+g.LocalBonus-g.LogScore) > 1e-10 {
					t.Fatal("分數拆解與解碼不符", g)
				}
			}
			if !x.Reachable || math.Abs(sum-x.LogScore) > 1e-10 {
				t.Fatal(x, sum)
			}
			for _, c := range d.CandidatesInContext(lat, 0, 1, nil, nil) {
				_, score := d.Best(lat, map[int]Lock{0: {Word: c.Entry.Surface, Len: 1}}, false)
				if math.Abs(score-c.Score) > 1e-10 {
					t.Fatal(score, c)
				}
			}
		}
	}
}

// 診斷：走了近音假設的詞，說明列出實際讀音、使用者打的音與通道成本；精確讀音的詞不列 typed。
func TestExplainReportsTypedReadingForCostedHypotheses(t *testing.T) {
	d := fixture()
	d.MaxCost = 2.4
	lat := input.Lattice{
		{{Syl: "ㄧㄣ"}, {Syl: "ㄧㄥ", Cost: 1.2}}, // 使用者打 ㄧㄣ，近音假設 ㄧㄥ
		{{Syl: "ㄍㄞ"}},
	}
	d.Score.(uni)["應該"] = 0.5 // 讓近音路徑勝出
	ex := d.ExplainBest(lat, nil, false)
	if len(ex.Segments) != 1 || ex.Segments[0].Word != "應該" || ex.Segments[0].Matched != "ㄧㄥ ㄍㄞ" {
		t.Fatalf("前提：%+v", ex.Segments)
	}
	c := ex.Segments[0].Contributions[0]
	if c.InputCost != 1.2 || len(c.Typed) != 2 || c.Typed[0] != "ㄧㄣ" || c.Typed[1] != "ㄍㄞ" || c.Reading[0] != "ㄧㄥ" {
		t.Fatalf("typed／reading／成本：%+v", c)
	}
	exact := d.ExplainBest(input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄣ ㄨㄟˋ")), nil, false)
	if exact.Segments[0].Matched != "" || exact.Segments[0].Contributions[0].Typed != nil {
		t.Fatalf("精確讀音不列 typed：%+v", exact.Segments[0])
	}
}
