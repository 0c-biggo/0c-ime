package compile

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestFallbackRequiresAgreementAndPreservesKnownRatios(t *testing.T) {
	uni := map[string]float64{"甲": .3, "乙": .3, "甲乙": .001, "丙": .099, "丁": .3}
	words := map[string]bool{}
	for w := range uni {
		words[w] = true
	}
	sources := []map[string]float64{
		{"甲": 40, "乙": 20, "甲乙": 20, "丙": 10, "丁": 10, "詞庫外": 1e9},
		{"甲": 20, "乙": 30, "甲乙": 30, "丁": 20},
	}
	n, err := fallbackUnigrams(uni, words, map[string]bool{"甲": true, "乙": true}, map[string]float64{"丁": 1}, sources, .25)
	if err != nil || n != 1 {
		t.Fatalf("only jointly supported missing word should change: %d %v", n, err)
	}
	if math.Abs(uni["甲乙"]-.05/1.049) > 1e-12 {
		t.Fatalf("unexpected fallback: %g", uni["甲乙"])
	}
	if math.Abs(uni["甲"]/uni["乙"]-1) > 1e-12 || math.Abs(uni["丙"]/uni["甲"]-.099/.3) > 1e-12 || math.Abs(uni["丁"]/uni["甲"]-1) > 1e-12 {
		t.Fatal("primary, single-source or explicit boost ratios changed")
	}
	sum := 0.
	for _, v := range uni {
		sum += v
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("not normalized: %g", sum)
	}
}

func TestFallbackInvalidInputDoesNotMutate(t *testing.T) {
	for _, tc := range []struct {
		scale   float64
		sources []map[string]float64
	}{
		{0, []map[string]float64{{"甲": 1}, {"甲": 1}}},
		{math.NaN(), []map[string]float64{{"甲": 1}, {"甲": 1}}},
		{math.Inf(1), []map[string]float64{{"甲": 1}, {"甲": 1}}},
		{1, []map[string]float64{{"甲": 1}}},
		{1, []map[string]float64{{"甲": 1}, {}}},
		{1, []map[string]float64{{"甲": 1}, {"甲": -1}}},
	} {
		u := map[string]float64{"甲": 1}
		if _, err := fallbackUnigrams(u, map[string]bool{"甲": true}, nil, nil, tc.sources, tc.scale); err == nil || u["甲"] != 1 {
			t.Fatalf("invalid input changed state or accepted: %+v", tc)
		}
	}
}

func TestBuildFallbackProvenanceAndDeterminism(t *testing.T) {
	d := load(t, fixture)
	d.Counts["toycorpus"] = map[string]float64{"丼": 100}
	d.Counts["imp"] = map[string]float64{"丼": 90, "親子丼": 10}
	d.Counts["nd"] = map[string]float64{"丼": 80, "親子丼": 20}
	o := DefaultOptions()
	o.Unigram = "toycorpus"
	base := Build(d, o)
	if len(base.Errors) > 0 {
		t.Fatal(base.Errors)
	}
	o.UnigramFallback = "imp,nd"
	o.FallbackScale = .25
	a := Build(d, o)
	if len(a.Errors) > 0 {
		t.Fatal(a.Errors)
	}
	if a.Report.FallbackWords != 1 || a.LM.Uni("親子丼") <= base.LM.Uni("親子丼") {
		t.Fatal("missing word did not improve")
	}
	if !bytes.Equal(a.Lexicon.Encode(), base.Lexicon.Encode()) {
		t.Fatal("frequency fallback changed readings")
	}
	o.UnigramFallback = "nd,imp"
	b := Build(d, o)
	if len(b.Errors) > 0 || !bytes.Equal(a.LM.Encode(), b.LM.Encode()) {
		t.Fatal("source order changed binary")
	}
	dir := t.TempDir()
	if err := a.Write(dir, "", o); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range m.Sources {
		seen[s.ID] = true
	}
	if !seen["imp"] || !seen["nd"] || m.Counts["fallback_words"] != 1 || m.Options.FallbackScale != .25 {
		t.Fatal("fallback provenance missing")
	}
	for _, spec := range []string{"imp", "imp,imp", "imp,missing"} {
		o.UnigramFallback = spec
		if len(Build(d, o).Errors) == 0 {
			t.Fatalf("accepted %q", spec)
		}
	}
}
