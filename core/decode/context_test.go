package decode

import (
	"math"
	"testing"

	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
)

type contextScore map[[2]string]float64

func (s contextScore) Prob(left, word string) float64 {
	if p := s[[2]string{left, word}]; p > 0 {
		return p
	}
	return 0.001
}

func TestCandidatesUseRightContextAndRespectOtherLocks(t *testing.T) {
	lex := lexicon.NewMem()
	for _, x := range []struct{ w, r string }{
		{"在", "ㄗㄞˋ"}, {"再", "ㄗㄞˋ"}, {"見", "ㄐㄧㄢˋ"}, {"建", "ㄐㄧㄢˋ"},
	} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
	}
	d := &Decoder{Lex: lex, Score: contextScore{
		{"<s>", "在"}: 0.6, {"<s>", "再"}: 0.4,
		{"再", "見"}: 0.8, {"在", "見"}: 0.01,
		{"在", "建"}: 0.2, {"再", "建"}: 0.001,
	}}
	d.Evidence = func(start, end int, left, word string) (float64, float64) {
		if start == 1 && left == "再" {
			if word == "見" {
				return .25, .25
			}
			return 0, .25
		}
		return 0, 0
	}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄗㄞˋ ㄐㄧㄢˋ"))
	if c := d.Candidates(lat, 0, 1, "<s>", nil); c[0].Entry.Surface != "在" {
		t.Fatal(c)
	}
	for _, tc := range []struct {
		locks map[int]Lock
		want  string
	}{
		{nil, "再"},
		{map[int]Lock{1: {Word: "建", Len: 1}}, "在"},
		// 明確重選本區間可以替換舊鎖定，右方仍固定。
		{map[int]Lock{0: {Word: "在", Len: 1}, 1: {Word: "見", Len: 1}}, "再"},
	} {
		got := d.CandidatesInContext(lat, 0, 1, tc.locks, nil)
		if got[0].Entry.Surface != tc.want {
			t.Fatalf("locks=%v: %+v", tc.locks, got)
		}
		// 與逐候選強制鎖定後的整段解碼對照；共享計算不得換了分數定義。
		for _, c := range got {
			locks := map[int]Lock{}
			for at, lock := range tc.locks {
				locks[at] = lock
			}
			locks[0] = Lock{Word: c.Entry.Surface, Len: 1}
			_, want := d.Best(lat, locks, false)
			if math.Abs(want-c.Score) > 1e-10 {
				t.Fatalf("%s: %g != %g", c.Entry.Surface, c.Score, want)
			}
		}
	}
}

func TestCandidateDedupKeepsBestReadingPath(t *testing.T) {
	lex := lexicon.NewMem()
	lex.Add(lexicon.Entry{Surface: "應", Reading: zhuyin.ParseReading("ㄧㄣ"), PRW: 0.1})
	lex.Add(lexicon.Entry{Surface: "應", Reading: zhuyin.ParseReading("ㄧㄥ"), PRW: 1})
	d := &Decoder{Lex: lex, Score: uni{"應": 1}}
	lat := input.Lattice{{{Syl: "ㄧㄣ"}, {Syl: "ㄧㄥ", Cost: 0.2}}}
	cs := d.CandidatesInContext(lat, 0, 1, nil, nil)
	if len(cs) != 1 || math.Abs(cs[0].Score+0.2) > 1e-10 {
		t.Fatal(cs)
	}
}

func TestContextRetainsAlternativeLeftSegmentations(t *testing.T) {
	lex := lexicon.NewMem()
	for _, x := range []struct{ w, r string }{
		{"甲", "ㄐㄧㄚˇ"}, {"假", "ㄐㄧㄚˇ"}, {"在", "ㄗㄞˋ"}, {"再", "ㄗㄞˋ"}, {"見", "ㄐㄧㄢˋ"},
	} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
	}
	d := &Decoder{Lex: lex, Score: contextScore{
		{"<s>", "甲"}: .6, {"<s>", "假"}: .4,
		{"甲", "在"}: .8, {"甲", "再"}: .001, {"假", "再"}: .9,
		{"在", "見"}: .1, {"再", "見"}: .9,
	}}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄐㄧㄚˇ ㄗㄞˋ ㄐㄧㄢˋ"))
	for _, locked := range []bool{false, true} {
		locks := map[int]Lock{}
		if locked {
			locks[0] = Lock{Word: "甲", Len: 1}
		}
		cs := d.CandidatesInContext(lat, 1, 1, locks, nil)
		want := "再"
		if locked {
			want = "在"
		}
		if cs[0].Entry.Surface != want {
			t.Fatalf("locked=%v: %+v", locked, cs)
		}
		for _, c := range cs {
			locks[1] = Lock{Word: c.Entry.Surface, Len: 1}
			_, sc := d.Best(lat, locks, false)
			if math.Abs(sc-c.Score) > 1e-10 {
				t.Fatalf("%s: %g != %g", c.Entry.Surface, c.Score, sc)
			}
		}
	}
}

func TestContextFallbackKeepsCandidatesWhenSuffixIsUnreachable(t *testing.T) {
	d := fixture()
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄥ ㄅㄨㄛˇ"))
	cs := d.CandidatesInContext(lat, 0, 1, nil, nil)
	if len(cs) == 0 || cs[0].Entry.Surface != "應" || math.IsInf(cs[0].Score, -1) {
		t.Fatal(cs)
	}
}
