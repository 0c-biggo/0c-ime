package main

import (
	"reflect"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

func fixture(partial bool) (*lexicon.Mem, *lm.Mem) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	entries := []struct {
		w, r string
		p    float64
	}{{"井", "ㄐㄧㄥˇ", .01}, {"景", "ㄐㄧㄥˇ", .001}}
	if partial {
		entries = append(entries, struct {
			w, r string
			p    float64
		}{"風景", "ㄈㄥ ㄐㄧㄥˇ", .5})
	} else {
		entries = append(entries, struct {
			w, r string
			p    float64
		}{"風", "ㄈㄥ", .5})
	}
	for _, e := range entries {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi("風", "景", .9)
	return lex, sys
}
func TestActualLeftAndTargetNeverInjected(t *testing.T) {
	lex, sys := fixture(false)
	c := sample{"x", "峰", "景", "ㄈㄥ", "ㄐㄧㄥˇ"}
	row, err := evaluate(lex, sys, .075, c)
	if err != nil {
		t.Fatal(err)
	}
	if row.LeftShown != "風" || row.Tail != "風" || !row.Confirmed || row.LeftCorrect {
		t.Fatalf("reference leaked into context: %+v", row)
	}
	if row.Production != "景" {
		t.Fatalf("context unused: %+v", row)
	}
	c.Want = "井"
	other, err := evaluate(lex, sys, .075, c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(row.Candidates, other.Candidates) {
		t.Fatal("target affected candidate generation")
	}
}
func TestPartialCommitHasNoContext(t *testing.T) {
	lex, sys := fixture(true)
	row, err := evaluate(lex, sys, .075, sample{"x", "風", "井", "ㄈㄥ", "ㄐㄧㄥˇ"})
	if err != nil {
		t.Fatal(err)
	}
	if row.Confirmed || row.Tail != "" || row.Production != "井" {
		t.Fatalf("partial tail used as confirmed word: %+v", row)
	}
}
func TestSplitPoint(t *testing.T) {
	if splitPoint([]string{"甲乙", "丙", "丁戊"}, 5) != 2 {
		t.Fatal("tie must use earlier word boundary")
	}
	if splitPoint([]string{"甲乙"}, 2) != 0 {
		t.Fatal("cannot split within a reference word")
	}
}
