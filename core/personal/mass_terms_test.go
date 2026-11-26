package personal

import (
	"math"
	"math/rand"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// MassTerms 跟分開呼叫 PriorMass、Prob 逐位元相同（學習前後、有沒有個人上下文、有沒有系統 bigram）；
// self 不是 Model 本身時（包著它的評分器）回傳 false。
func TestMassTermsMatchSeparateCalls(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	words := []string{"甲", "乙", "丙", "丁", "甲乙", "丙丁", "戊"}
	sys := lm.NewMem()
	for _, w := range words {
		sys.SetUni(w, math.Exp(-3-6*rng.Float64()))
	}
	for range 12 {
		sys.SetBi(words[rng.Intn(len(words))], words[rng.Intn(len(words))], math.Exp(-1-4*rng.Float64()))
	}
	m := NewModel(lexicon.NewMem(), sys)
	check := func(when string) {
		for _, l := range append([]string{lm.BOS, "未知"}, words...) {
			for _, w := range words {
				prior, prob, ok := m.MassTerms(m, l, w)
				if !ok || prior != m.PriorMass(l, w) || prob != m.Prob(l, w) {
					t.Fatalf("%s %s→%s: MassTerms %v %v %v, separate %v %v", when, l, w, prior, prob, ok, m.PriorMass(l, w), m.Prob(l, w))
				}
			}
		}
	}
	check("before learning")
	for range 10 {
		a, b := words[rng.Intn(4)], words[rng.Intn(4)]
		m.Learn(Commit{Syls: zhuyin.ParseReading("ㄐㄧㄚˇ ㄧˇ"), Path: []Seg{{Word: a, End: 1, Manual: true}, {Word: b, Start: 1, End: 2}}})
	}
	check("after learning")
	type wrapper struct{ *Model }
	if _, _, ok := m.MassTerms(&wrapper{m}, "甲", "乙"); ok {
		t.Fatal("a wrapping scorer must fall back to its own PriorMass and Prob")
	}
}
