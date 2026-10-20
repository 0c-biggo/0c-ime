package decode

import (
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

type nbScore struct {
	contextScore
	contextual bool
}

func (s nbScore) HasContext(string) bool { return s.contextual }

func nbLex(pairs ...string) *lexicon.Mem {
	l := lexicon.NewMem()
	for i := 0; i < len(pairs); i += 2 {
		l.Add(lexicon.Entry{Surface: pairs[i], Reading: zhuyin.ParseReading(pairs[i+1]), PRW: 1})
	}
	return l
}

func TestNBestKeepsHistoriesAndDeduplicatesSegmentations(t *testing.T) {
	l := nbLex("甲", "ㄐㄧㄚˇ", "假", "ㄐㄧㄚˇ", "乙", "ㄧˇ", "甲乙", "ㄐㄧㄚˇ ㄧˇ")
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"))
	for _, contextual := range []bool{false, true} {
		d := Decoder{Lex: l, Score: nbScore{contextScore{}, contextual}}
		rows, _ := d.NBest(lat, nil, false, 8)
		if len(rows) != 2 || rows[0].Text != "甲乙" || rows[1].Text != "假乙" {
			t.Fatal(rows)
		}
		locked, _ := d.NBest(lat, map[int]Lock{0: {Word: "假", Len: 1}}, false, 8)
		if len(locked) != 1 || locked[0].Text != "假乙" {
			t.Fatal(locked)
		}
	}
}

func TestNBestUnprunedMatchesExhaustivePaths(t *testing.T) {
	// 獨立枚舉所有完整詞路徑，驗證不是只回傳各前詞的一條最佳路徑。
	l := nbLex("甲", "ㄚ", "乙", "ㄚ", "丙", "ㄚ", "甲甲", "ㄚ ㄚ", "乙甲", "ㄚ ㄚ")
	entries := l.Prefixed(zhuyin.ParseReading("ㄚ"))
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄚ ㄚ ㄚ ㄚ"))
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		score := contextScore{}
		for _, left := range []string{"<s>", "甲", "乙", "丙", "甲甲", "乙甲"} {
			for _, e := range entries {
				score[[2]string{left, e.Surface}] = .001 + rng.Float64()*.9
			}
		}
		all := map[string]float64{}
		// 非詞疊字（#139）：兩個相同單字、疊字不是詞時，P(X)·P(X|X) 最多是兩音節詞的 P(詞)／e，
		// 先驗不含前文（這個評分器沒有 Prior，用句首機率）。
		// 詞庫詞（或兩個單字拼出的同一字面）要勝過其他兩個單字的讀法 P(A)·P(B|A)（AB ≠ XX）才設上限（#140）。
		repeatCap := func(x string) float64 {
			word := math.Inf(-1)
			for _, e := range entries {
				if len(e.Reading) == 2 {
					word = max(word, math.Log(score.Prob("<s>", e.Surface)*e.PRW))
				}
			}
			lex, other := word, math.Inf(-1)
			for _, a := range entries {
				for _, b := range entries {
					if len(a.Reading) != 1 || len(b.Reading) != 1 || a.Surface == x && b.Surface == x {
						continue
					}
					v := math.Log(score.Prob("<s>", a.Surface) * score.Prob(a.Surface, b.Surface))
					if l.Has(a.Surface + b.Surface) {
						lex = max(lex, v)
					} else {
						other = max(other, v)
					}
				}
			}
			if lex <= other {
				return math.Inf(1)
			}
			return word - math.Log(score.Prob("<s>", x)) - repeatMargin
		}
		var walk func(int, string, bool, string, float64)
		walk = func(at int, left string, single bool, text string, value float64) {
			if at == len(lat) {
				if v, ok := all[text]; !ok || value > v {
					all[text] = value
				}
				return
			}
			for _, e := range entries {
				if at+len(e.Reading) <= len(lat) {
					arc := math.Log(score.Prob(left, e.Surface) * e.PRW)
					if single && len(e.Reading) == 1 && e.Surface == left && !l.Has(left+left) {
						arc = min(arc, repeatCap(left))
					}
					walk(at+len(e.Reading), e.Surface, len(e.Reading) == 1, text+e.Surface, value+arc)
				}
			}
		}
		walk(0, "<s>", false, "", 0)
		var want []Hypothesis
		for text, score := range all {
			want = append(want, Hypothesis{Text: text, LogScore: score})
		}
		sort.Slice(want, func(i, j int) bool { return want[i].LogScore > want[j].LogScore })
		d := Decoder{Lex: l, Score: score}
		for _, k := range []int{1, 8, 16, 32} {
			got, _ := d.NBest(lat, nil, false, k)
			if len(got) != k {
				t.Fatal(seed, k, len(got))
			}
			for i, row := range got {
				// 同分只要求同一分數與各字串真實最佳分數，不依 map 次序選答案。
				if math.Abs(row.LogScore-want[i].LogScore) > 1e-12 || math.Abs(all[row.Text]-row.LogScore) > 1e-12 {
					t.Fatalf("seed %d K%d #%d: %+v want score %g", seed, k, i, row, want[i].LogScore)
				}
				audit := d.AuditTarget(lat, nil, false, row.Text)
				if !audit.ReadingReachable || audit.Best == nil || math.Abs(audit.Best.LogScore-row.LogScore) > 1e-12 {
					t.Fatalf("target oracle disagrees: %+v vs %+v", audit, row)
				}
			}
		}
	}
}

func TestNBestReadingCostsAndPrefixDedup(t *testing.T) {
	l := nbLex("應", "ㄧㄣ", "應", "ㄧㄥ", "鷹", "ㄧㄥ", "應該", "ㄧㄥ ㄍㄞ")
	lat := input.Lattice{{{Syl: "ㄧㄣ", Cost: 2}, {Syl: "ㄧㄥ", Cost: .2}}}
	d := Decoder{Lex: l, Score: uni{"應": .3, "鷹": .2, "應該": .9}, SeparateContinuation: true, Continue: .5}
	rows, _ := d.NBest(lat, nil, true, 8)
	if len(rows) != 2 || rows[0].Text != "應" || !rows[0].Segments[0].Partial || math.Abs(rows[0].LogScore-(math.Log(.9*.5)-.2)) > 1e-12 {
		t.Fatal(rows)
	}
	best, sc := d.Best(lat, nil, true)
	if rows[0].Segments[0].Word != best[0].Word || math.Abs(sc-rows[0].LogScore) > 1e-12 {
		t.Fatal(rows, best, sc)
	}
	for i := 0; i < 10; i++ {
		again, _ := d.NBest(lat, nil, true, 8)
		if !reflect.DeepEqual(rows, again) {
			t.Fatal("non-deterministic")
		}
	}
}

func TestNBestPruningAndTargetLayers(t *testing.T) {
	l := nbLex("甲", "ㄚ", "乙", "ㄚ", "丙", "ㄅ")
	s := nbScore{contextScore{{"<s>", "甲"}: .9, {"<s>", "乙"}: .1, {"甲", "丙"}: .001, {"乙", "丙"}: .9}, true}
	d := Decoder{Lex: l, Score: s, Beam: 1}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄚ ㄅ"))
	rows, stats := d.NBest(lat, nil, false, 8)
	if len(rows) != 1 || rows[0].Text != "甲丙" || stats.PrunedContexts == 0 {
		t.Fatal(rows, stats)
	}
	audit := d.AuditTarget(lat, nil, false, "乙丙")
	if !audit.SurfaceSegmentable || !audit.ReadingReachable || audit.Best == nil || audit.Best.LogScore <= rows[0].LogScore {
		t.Fatal(audit)
	}
	d.Beam = 0
	rows, _ = d.NBest(lat, nil, false, 8)
	if len(rows) != 2 || rows[0].Text != "乙丙" {
		t.Fatal(rows)
	}
	if a := d.AuditTarget(lat, nil, false, "丙甲"); !a.SurfaceSegmentable || a.ReadingReachable || a.Best != nil {
		t.Fatal(a)
	}
	if a := d.AuditTarget(lat, nil, false, "未知"); a.SurfaceSegmentable || a.ReadingReachable || a.Best != nil {
		t.Fatal(a)
	}
	l = nbLex("甲乙", "ㄚ ㄅ", "丙丁", "ㄚ ㄅ")
	d = Decoder{Lex: l, Score: uni{"甲乙": .9, "丙丁": .1}, TopM: 1, SeparateContinuation: true}
	short := input.Exact{}.Expand(zhuyin.ParseReading("ㄚ"))
	a := d.AuditTarget(short, nil, true, "丙")
	if !a.ReadingReachable || a.Best != nil {
		t.Fatal("expected prefix pool limit", a)
	}
}

func TestNBestNestedListsAndEmpty(t *testing.T) {
	l := nbLex("甲", "ㄚ", "乙", "ㄚ", "丙", "ㄚ")
	d := Decoder{Lex: l, Score: uni{"甲": .5, "乙": .3, "丙": .2}, Beam: 8}
	lat := input.Exact{}.Expand(zhuyin.ParseReading(strings.TrimSpace(strings.Repeat("ㄚ ", 5))))
	full, _ := d.NBest(lat, nil, true, 32)
	for _, k := range []int{8, 16} {
		rows, _ := d.NBest(lat, nil, true, k)
		if !reflect.DeepEqual(rows, full[:k]) {
			t.Fatal("lists not nested", k)
		}
	}
	if rows, _ := d.NBest(nil, nil, true, 8); len(rows) != 0 {
		t.Fatal(rows)
	}
	if rows, _ := d.NBest(lat, nil, true, 0); len(rows) != 0 {
		t.Fatal(rows)
	}
}
