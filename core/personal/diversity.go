package personal

import (
	"math"
	"strings"
)

// diversityIndex 保存每個前文的「後接詞多樣性」Σ_w min(WPassive, c(L,w))。
// 以 1e-9 為單位的整數累加：增量更新與整份重算結果完全相同，也不受 map 走訪順序影響。
// observe／dropBi 增量維護；衰減（每小時至多一次）、換 Store，或計數筆數／總量被直接改動時整份重算。
type diversityIndex struct {
	store  *Store
	bi     int
	total  float64
	unit   float64
	byLeft map[string]int64
}

const diversityScale = 1e9

func (m *Model) diversityUnits(v float64) int64 {
	if !(v > 0) {
		return 0
	}
	return int64(math.Round(min(m.P.WPassive, v) * diversityScale))
}

// kappa 是前文 left 的先驗等效樣本數：KappaBi 與後接詞多樣性取大（見 Params.TypePrior）。
func (m *Model) kappa(left string) float64 {
	k := m.P.KappaBi
	if m.P.TypePrior > 0 {
		k = max(k, m.P.TypePrior*m.diversity(left))
	}
	return k
}

func (m *Model) diversityValid() bool {
	idx := &m.diversityIndex
	s := m.Store
	return idx.byLeft != nil && idx.store == s && idx.bi == len(s.Bi) && idx.total == s.Total && idx.unit == m.P.WPassive
}

func (m *Model) diversity(left string) float64 {
	if !m.diversityValid() {
		idx := &m.diversityIndex
		s := m.Store
		*idx = diversityIndex{store: s, bi: len(s.Bi), total: s.Total, unit: m.P.WPassive, byLeft: map[string]int64{}}
		for k, v := range s.Bi {
			if l, _, ok := strings.Cut(k, "\t"); ok {
				idx.byLeft[l] += m.diversityUnits(v)
			}
		}
	}
	return float64(m.diversityIndex.byLeft[left]) / diversityScale
}

// changeBi 在計數改變後增量更新多樣性；呼叫端須在改動前確認索引有效（diversityValid），
// 否則不呼叫，留給下次查詢整份重算。
func (m *Model) changeBi(left string, before, after float64) {
	idx := &m.diversityIndex
	idx.byLeft[left] += m.diversityUnits(after) - m.diversityUnits(before)
	idx.bi, idx.total = len(m.Store.Bi), m.Store.Total
}
