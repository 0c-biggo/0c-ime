package personal

import (
	"fmt"
	"maps"
	"reflect"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

func diversityModel() *Model {
	sys := lm.NewMem()
	sys.SetUni("沒有", 0.002)
	sys.SetBi("在", "沒有", 0.002)
	sys.SetUni("丼", 0.0001)
	m := NewModel(lexicon.NewMem(), sys)
	m.AdvanceTime(100 * daySeconds)
	return m
}

// 常用字後面接過很多種詞時，沒接過的詞仍保有可觀的系統機率；舊算法只剩 1/(c+1)。
func TestFrequentLeftWordKeepsUnseenContinuations(t *testing.T) {
	ratio := func(typePrior float64) float64 {
		m := diversityModel()
		m.P.TypePrior = typePrior
		for i := range 30 {
			m.observe("在", fmt.Sprintf("詞%d", i), m.P.WPassive)
		}
		return m.Prob("在", "沒有") / m.SystemProb("在", "沒有")
	}
	if old := ratio(0); old > 0.15 {
		t.Fatalf("前提：舊算法把沒接過的詞壓到 1/(c+1)：%.3f", old)
	}
	if got := ratio(DefaultParams().TypePrior); got < 0.45 {
		t.Fatalf("常用前文接過 30 種詞，沒接過的詞不應被壓得這麼低：%.3f", got)
	}
}

// 稀疏前文一次觀察即有力：只接過一兩種詞時與舊算法相同。
func TestSparseContextUnchanged(t *testing.T) {
	probs := map[float64]float64{}
	for _, typePrior := range []float64{0, 1} {
		m := diversityModel()
		m.P.TypePrior = typePrior
		for range 5 {
			m.observe("親子", "景", m.P.WPassive)
		}
		m.observe("親子", "丼", m.P.WPassive)
		probs[typePrior] = m.Prob("親子", "丼")
	}
	if probs[0] != probs[1] {
		t.Fatalf("稀疏前文的機率不應改變：%v", probs)
	}
}

// 衰減時只出現過一次的後接詞同比例淡出，先驗比例不變；常用詞對的多樣性貢獻有上限，
// 閒置越久個人計數相對先驗越弱，方向與 30 日衰減的原意一致。直接改計數也會使快取失效。
func TestDiversityFollowsDecayAndDirectEdits(t *testing.T) {
	m := diversityModel()
	for i := range 30 {
		m.observe("在", fmt.Sprintf("詞%d", i), m.P.WPassive)
	}
	before := m.Prob("在", "沒有")
	m.AdvanceTime(m.Store.Time + 60*daySeconds)
	if after := m.Prob("在", "沒有"); after < before*0.99 || after > before*1.01 {
		t.Fatalf("單次觀察的後接詞衰減後先驗比例應維持：%.6f → %.6f", before, after)
	}

	m = diversityModel()
	for range 100 {
		m.observe("在", "這裡", m.P.WPassive)
	}
	for i := range 10 {
		m.observe("在", fmt.Sprintf("詞%d", i), m.P.WPassive)
	}
	before = m.Prob("在", "沒有")
	m.AdvanceTime(m.Store.Time + 60*daySeconds)
	if after := m.Prob("在", "沒有"); after <= before {
		t.Fatalf("閒置後個人計數應相對變弱：%.6f → %.6f", before, after)
	}

	m.Store.Bi[pair("在", "新詞")] = 0.25
	m.Store.Ctx["在"] += 0.25
	want := m.diversity("在")
	m.diversityIndex = diversityIndex{}
	if got := m.diversity("在"); got != want {
		t.Fatalf("直接新增計數後應重算多樣性：%v，重算 %v", want, got)
	}
}

// 提交後增量更新，與整份重算完全相同，且不觸發重算（撤銷學習會改總量，下次查詢整份重算）。
func TestDiversityIncrementalMatchesRebuild(t *testing.T) {
	m := diversityModel()
	m.observe("在", "這裡", m.P.WPassive)
	m.diversity("在")
	built := reflect.ValueOf(m.diversityIndex.byLeft).UnsafePointer()
	for i := range 50 {
		m.Learn(Commit{Syls: zhuyin.ParseReading("ㄗㄞˋ ㄓㄜˋ ㄌㄧˇ"), Path: []Seg{{Word: "在", End: 1}, {Word: fmt.Sprintf("這%d", i%7), Start: 1, End: 3}}})
	}
	if !m.diversityValid() || reflect.ValueOf(m.diversityIndex.byLeft).UnsafePointer() != built {
		t.Fatal("提交後應增量更新，不整份重算")
	}
	same := func() {
		t.Helper()
		incremental := maps.Clone(m.diversityIndex.byLeft)
		m.diversityIndex = diversityIndex{}
		m.diversity("在")
		if !maps.Equal(incremental, m.diversityIndex.byLeft) {
			t.Fatalf("增量 %v，重算 %v", incremental, m.diversityIndex.byLeft)
		}
	}
	same()
	m.Forget("這3")
	m.diversity("在")
	m.Learn(Commit{Syls: zhuyin.ParseReading("ㄗㄞˋ ㄓㄜˋ ㄌㄧˇ"), Path: []Seg{{Word: "在", End: 1}, {Word: "這8", Start: 1, End: 3}}})
	same()
}
