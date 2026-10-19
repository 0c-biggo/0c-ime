package compile

import "testing"

func TestRelativeReadingsPreservesWithinWordCostsAndProvenance(t *testing.T) {
	a := map[key]*entry{
		{"甲", "a"}: {word: "甲", prw: .1, grade: Attested, origins: map[string]bool{"source": true}},
		{"甲", "b"}: {word: "甲", prw: .02, grade: Attested},
		{"乙", "a"}: {word: "乙", prw: 1, grade: Primary},
		{"乙", "b"}: {word: "乙", prw: .1, grade: Attested},
		{"甲", "c"}: {word: "甲", prw: .3, grade: Correction, correct: true},
	}
	normalizeReadings(a)
	for k, want := range map[key]float64{{"甲", "a"}: 1, {"甲", "b"}: .2, {"乙", "a"}: 1, {"乙", "b"}: .1, {"甲", "c"}: .3} {
		got := a[k].prw
		if got < want-1e-12 || got > want+1e-12 {
			t.Fatal(k, got, want)
		}
	}
	if e := a[key{"甲", "a"}]; e.grade != Attested || !e.origins["source"] {
		t.Fatal("權重不能改寫來源證據")
	}
}
