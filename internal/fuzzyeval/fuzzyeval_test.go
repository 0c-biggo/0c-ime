package fuzzyeval

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func allSelect() fuzzy.Config {
	var c fuzzy.Config
	for _, p := range fuzzy.Pairs() {
		c = c.With(p, fuzzy.Select)
	}
	return c
}

func TestNoiseIsDeterministicLegalAndOnlyTouchesEligibleSyllables(t *testing.T) {
	reading := zhuyin.ParseReading("ㄓ ㄕˋ ㄅㄚˋ ㄔ ㄈㄢˋ ㄧㄥ ㄍㄞ")
	a, sa := Noise(allSelect(), reading, 1, rand.New(rand.NewSource(7)))
	b, sb := Noise(allSelect(), reading, 1, rand.New(rand.NewSource(7)))
	if !slices.Equal(a, b) || !slices.Equal(sa, sb) {
		t.Fatal("同一個種子必須得到同一份錯音")
	}
	if slices.Contains(sa, 2) || slices.Contains(sa, 4) {
		t.Errorf("沒有近音替代的音節（ㄅㄚˋ、ㄈㄢˋ）不得被換：%v", sa)
	}
	for _, at := range sa {
		if a[at] == reading[at] || !a[at].Legal() {
			t.Errorf("位置 %d：%s → %s 不是合法的近音", at, reading[at], a[at])
		}
	}
	if n, _ := Noise(allSelect(), reading, 0, rand.New(rand.NewSource(7))); !slices.Equal(n, reading) {
		t.Error("比例 0 不得改動")
	}
	if !Eligible(allSelect(), reading) || Eligible(allSelect(), zhuyin.ParseReading("ㄅㄚˋ ㄈㄢˋ")) {
		t.Error("Eligible")
	}
}

func toyEngine(Arm) (*engine.Engine, error) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, w := range []struct {
		w, r string
		p    float64
	}{{"吃飯", "ㄔ ㄈㄢˋ", 1e-3}, {"飯", "ㄈㄢˋ", 2e-3}, {"吃", "ㄔ", 2e-3}, {"疵", "ㄘ", 1e-6}, {"知識", "ㄓ ㄕˋ", 1e-3}, {"資", "ㄗ", 1e-6}, {"是", "ㄕˋ", 1e-6}} {
		lex.Add(lexicon.Entry{Surface: w.w, Reading: zhuyin.ParseReading(w.r), PRW: 1})
		sys.SetUni(w.w, w.p)
	}
	return engine.New(lex, sys), nil
}

// 端到端：玩具詞庫上，錯音輸入在原版本是錯的，auto 修回來；乾淨輸入兩邊都對、auto 沒有誤改；
// 僅供選字能在候選中找到目標詞。
func TestRunOnToyLexicon(t *testing.T) {
	cases := []Case{
		{Ref: "吃飯", Reading: zhuyin.ParseReading("ㄔ ㄈㄢˋ"), Words: []string{"吃飯"}},
		{Ref: "知識", Reading: zhuyin.ParseReading("ㄓ ㄕˋ"), Words: []string{"知識"}},
	}
	var auto fuzzy.Config
	for _, p := range fuzzy.Pairs() {
		auto = auto.With(p, fuzzy.Auto)
	}
	p := Params{Auto: auto, Policy: fuzzy.DefaultAutoPolicy(), Rate: 1, Seed: 3}
	bases, err := RunBase(toyEngine, cases, p, 2)
	if err != nil {
		t.Fatal(err)
	}
	autos, err := RunAuto(toyEngine, bases, p, 2)
	if err != nil {
		t.Fatal(err)
	}
	r := Summarize(bases, autos)
	if r.Sentences != 2 || r.Noisy.Sentences != 2 {
		t.Fatalf("句數：%+v", r)
	}
	if r.Clean.Applicable != 2 || r.Clean.FalseChanges != 0 {
		t.Errorf("乾淨輸入：%+v", r.Clean)
	}
	if r.Noisy.OffExact != 0 || r.Noisy.AutoExact != 2 || r.Noisy.Fixed != 2 || r.Noisy.Harmed != 0 {
		t.Errorf("錯音輸入：%+v", r.Noisy)
	}
	if r.Noisy.RepairRate != 1 || r.Noisy.Precision != 1 || r.Noisy.AutoCER != 0 || r.Noisy.OffCER == 0 {
		t.Errorf("比率：%+v", r.Noisy)
	}
	if r.Recall.Words != 2 || r.Recall.Any != 1 || r.Recall.AtPage1 != 1 {
		t.Errorf("候選可達：%+v", r.Recall)
	}
}

// 門檻拉到不可能達到時，auto 什麼都不改，等同原版本：誤改與修復都是零。
func TestRunWithAnImpossibleThresholdChangesNothing(t *testing.T) {
	cases := []Case{{Ref: "吃飯", Reading: zhuyin.ParseReading("ㄔ ㄈㄢˋ"), Words: []string{"吃飯"}}}
	p := Params{Auto: fuzzy.Config{}.With(fuzzy.CCh, fuzzy.Auto), Policy: fuzzy.AutoPolicy{Margin: 1e9}, Rate: 1, Seed: 1}
	bases, err := RunBase(toyEngine, cases, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	autos, _ := RunAuto(toyEngine, bases, p, 1)
	r := Summarize(bases, autos)
	if r.Noisy.Fixed != 0 || r.Noisy.Changed != 0 || r.Clean.FalseChanges != 0 {
		t.Fatalf("%+v", r)
	}
}
