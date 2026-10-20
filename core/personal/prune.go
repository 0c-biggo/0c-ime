package personal

import (
	"sort"
	"strings"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 個人資料清理。
//
// 排序計數與補全證據會隨時間衰減，但衰減只是乘上係數，項目本身不會消失；不清理的話，
// 只用過一次的詞對會永遠留在 user.json 裡，檔案一年可長到數十 MB。清理只刪「拿掉也幾乎
// 不改變分數」的項目，並為每一類資料設一個寬上限。明確的選擇（更正、親手選過的讀音與單字、
// 個人片語、刪除後的 tombstone、被推翻的自動修正）不在清理範圍內，一筆都不刪。

// PrunePolicy 是清理門檻。
type PrunePolicy struct {
	// Share：排序計數 c（Bi[L,w] 或 Uni[w]）小於系統先驗的等效計數（κ·P_sys）乘上這個比例時，
	// 拿掉它讓這個詞的機率改變不到 Share（自然對數約同值），視為不影響排序。
	//
	// 不能只看衰減到多小：系統先驗常比衰減一年的計數還小，半年前用過一次的詞對仍會決定
	// 同音詞或斷詞的先後。因此以相對於系統先驗的影響決定是否清理，其餘由容量上限處理。
	Share float64
	// Margin：超過上限要淘汰詞對計數時，先淘汰「在或不在都不改變這個詞與同音候選先後」的
	// （雙方分數區間至少相隔 Margin 倍），再淘汰其他；各自由衰減後最弱者開始。
	Margin float64
	// Evidence：補全證據衰減後的 Short+Long 低於此值即清掉。補全信心是
	// (c + 5q) / (Σc + 5)，拿掉 c 讓信心改變不超過 c/5；證據不參與整句選字。
	Evidence float64
	// MaxPairs：Bi 最多幾筆；MaxEvidence：EvidenceByContext 最多幾筆；
	// MaxWord：每個詞的資料（Uni、EvidenceByWord、暫定片語）各最多幾筆。
	// 超過時淘汰衰減後最弱、未受保護的項目。是保險，一般使用不會碰到。
	MaxPairs, MaxEvidence, MaxWord int
}

// DefaultPrunePolicy 回傳排序影響門檻、證據門檻與各類資料的容量上限。
func DefaultPrunePolicy() PrunePolicy {
	return PrunePolicy{Share: 0.003, Margin: 1.05, Evidence: 0.05, MaxPairs: 200000, MaxEvidence: 60000, MaxWord: 30000}
}

// PruneStats 是一次清理刪掉的筆數，不含任何文字。
type PruneStats struct {
	Bi, Ctx, Uni, Uses, EvidenceByContext, EvidenceByWord, PhraseSeen int
}

func (p PruneStats) Total() int {
	return p.Bi + p.Ctx + p.Uni + p.Uses + p.EvidenceByContext + p.EvidenceByWord + p.PhraseSeen
}

// Prune 以預設門檻清理；見 PruneWith。
func (m *Model) Prune() PruneStats { return m.PruneWith(DefaultPrunePolicy()) }

// PruneIfDue 每個 UTC 日（依個人資料時間）至多清理一次，供宿主在存檔前呼叫。
// 清理日不寫進個人檔：重啟後第一次存檔再清一次，結果相同，不必改變存檔格式。
func (m *Model) PruneIfDue() (PruneStats, bool) {
	day := m.Store.Time/daySeconds + 1 // +1：讓零值代表「這個行程還沒清過」
	if m.prunedDay == day {
		return PruneStats{}, false
	}
	m.prunedDay = day
	return m.Prune(), true
}

// protectedWords 是明確選擇涉及的詞：它們的詞層級資料（Uni、Uses、EvidenceByWord）不清，
// 個人片語連同各前文的計數與證據也不清（系統沒收錄的片語只靠這些計數排序）。
func (m *Model) protectedWords() (words, phrases map[string]bool) {
	s := m.Store
	words, phrases = map[string]bool{}, map[string]bool{}
	for ph := range s.Phrases {
		words[ph], phrases[ph] = true, true
	}
	for k := range s.Readings {
		w, _, _ := strings.Cut(k, "\t")
		words[w] = true
	}
	for _, ws := range s.Picked {
		for _, w := range ws {
			words[w] = true
		}
	}
	for _, c := range s.Corrections {
		words[c.Word] = true
	}
	return words, phrases
}

// PruneWith 刪除衰減到不影響排序與補全的計數和證據，再對各類資料套用上限。
// 所有判斷都以清理前的狀態計算，與 map 走訪順序無關，結果可重現。
func (m *Model) PruneWith(p PrunePolicy) PruneStats {
	s := m.Store
	var st PruneStats
	words, phrases := m.protectedWords()

	// 排序計數：Bi 與 Ctx 一起改，保持 Ctx[L] = Σ Bi[L,·]。
	type weak struct {
		key   string
		value float64
	}
	byValue := func(xs []weak) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].value != xs[j].value {
				return xs[i].value < xs[j].value
			}
			return xs[i].key < xs[j].key
		})
	}
	kappas := map[string]float64{}
	kappa := func(left string) float64 {
		k, ok := kappas[left]
		if !ok {
			k = m.kappa(left)
			kappas[left] = k
		}
		return k
	}
	ranks := m.rankChecker(p.Margin)
	dropBi := map[string]bool{}
	var keepBi []weak
	for k, c := range s.Bi {
		left, w, _ := strings.Cut(k, "\t")
		if phrases[w] {
			continue
		}
		if c <= p.Share*ranks.prior(left, w, kappa(left)) {
			dropBi[k] = true
		} else {
			keepBi = append(keepBi, weak{k, c})
		}
	}
	if over := len(s.Bi) - len(dropBi) - p.MaxPairs; p.MaxPairs > 0 && over > 0 {
		var safe, other []weak
		for _, x := range keepBi {
			left, w, _ := strings.Cut(x.key, "\t")
			if ranks.safe(left, w, x.value, kappa(left)) {
				safe = append(safe, x)
			} else {
				other = append(other, x)
			}
		}
		byValue(safe)
		byValue(other)
		for _, x := range append(safe, other...)[:min(over, len(keepBi))] {
			dropBi[x.key] = true
		}
	}
	for k := range dropBi {
		left, _, _ := strings.Cut(k, "\t")
		s.Ctx[left] -= s.Bi[k]
		delete(s.Bi, k)
		st.Bi++
	}
	// 前文底下的詞對都清掉後，Ctx 只剩浮點殘差（Ctx[L] 一直等於 Σ Bi[L,·]）。
	lefts := map[string]bool{}
	for k := range s.Bi {
		left, _, _ := strings.Cut(k, "\t")
		lefts[left] = true
	}
	for left := range s.Ctx {
		if !lefts[left] {
			delete(s.Ctx, left)
			st.Ctx++
		}
	}

	// 無上下文計數：Uni 與 Total 一起改；舊版 Uses 不衰減、不再使用，跟著 Uni 走。
	var keepUni []weak
	dropUni := map[string]bool{}
	for w, c := range s.Uni {
		if words[w] {
			continue
		}
		if c <= p.Share*m.P.KappaUni*m.LM.Uni(w) {
			dropUni[w] = true
		} else {
			keepUni = append(keepUni, weak{w, c})
		}
	}
	if over := len(s.Uni) - len(dropUni) - p.MaxWord; p.MaxWord > 0 && over > 0 {
		byValue(keepUni)
		for _, x := range keepUni[:min(over, len(keepUni))] {
			dropUni[x.key] = true
		}
	}
	for w := range dropUni {
		s.Total -= s.Uni[w]
		delete(s.Uni, w)
		st.Uni++
	}
	if s.Total < 0 || len(s.Uni) == 0 {
		s.Total = 0
	}
	for w := range s.Uses {
		if _, ok := s.Uni[w]; !ok && !words[w] {
			delete(s.Uses, w)
			st.Uses++
		}
	}

	// 補全證據。
	st.EvidenceByContext = pruneEvidence(s.EvidenceByContext, s.Time, p.Evidence, p.MaxEvidence, func(k string) bool {
		_, w, _ := strings.Cut(k, "\t")
		return phrases[w]
	})
	st.EvidenceByWord = pruneEvidence(s.EvidenceByWord, s.Time, p.Evidence, p.MaxWord, func(w string) bool { return words[w] })

	// 尚未晉升的暫定片語：它的證據已淡出就不再等第二次出現。已晉升的保留。
	var keepSeen []weak
	for ph := range s.PhraseSeen {
		if phrases[ph] {
			continue
		}
		if _, ok := s.EvidenceByWord[ph]; !ok {
			delete(s.PhraseSeen, ph)
			st.PhraseSeen++
			continue
		}
		e := s.EvidenceByWord[ph].at(s.Time)
		keepSeen = append(keepSeen, weak{ph, e.Short + e.Long})
	}
	if over := len(keepSeen) - p.MaxWord; p.MaxWord > 0 && over > 0 {
		byValue(keepSeen)
		for _, x := range keepSeen[:over] {
			delete(s.PhraseSeen, x.key)
			st.PhraseSeen++
		}
	}

	if st.Total() > 0 {
		m.Gen++
	}
	return st
}

// pruneEvidence 刪除衰減後低於 floor 的證據，超過上限時再刪最弱者；protected 的項目都保留。
func pruneEvidence(evidence map[string]UsageEvidence, now int64, floor float64, limit int, protected func(string) bool) int {
	type weak struct {
		key   string
		value float64
	}
	var keep []weak
	removed := 0
	for k, e := range evidence {
		if protected(k) {
			continue
		}
		e = e.at(now)
		if v := e.Short + e.Long; v < floor {
			delete(evidence, k)
			removed++
		} else {
			keep = append(keep, weak{k, v})
		}
	}
	if over := len(evidence) - limit; limit > 0 && over > 0 {
		sort.Slice(keep, func(i, j int) bool {
			if keep[i].value != keep[j].value {
				return keep[i].value < keep[j].value
			}
			return keep[i].key < keep[j].key
		})
		for _, x := range keep[:min(over, len(keep))] {
			delete(evidence, x.key)
			removed++
		}
	}
	return removed
}

// rankChecker 判斷拿掉一筆 Bi 計數會不會改變同音候選的先後；同一讀音的候選查一次就快取。
type rankChecker struct {
	m      *Model
	lookup interface{ Lookup(string) []lexicon.Entry }
	src    lexicon.Source
	margin float64
	homo   map[string]map[string]float64 // 讀音鍵 → 詞面 → 最大 P(r|w)
}

func (m *Model) rankChecker(margin float64) *rankChecker {
	r := &rankChecker{m: m, src: m.Source(), margin: margin, homo: map[string]map[string]float64{}}
	r.lookup, _ = m.Lex.(interface{ Lookup(string) []lexicon.Entry })
	return r
}

func (r *rankChecker) homophones(reading []zhuyin.Syllable) map[string]float64 {
	k := zhuyin.Key(reading)
	if h, ok := r.homo[k]; ok {
		return h
	}
	h := map[string]float64{}
	for _, e := range r.src.Exact(reading) {
		if !e.Correction {
			h[e.Surface] = max(h[e.Surface], e.PRW)
		}
	}
	r.homo[k] = h
	return h
}

// prior 是 Prob 分子中與 Bi 無關的部分（κ·P_sys，含個人 Uni）；同一前文的分母相同，可直接比較。
func (r *rankChecker) prior(left, w string, kappa float64) float64 {
	m, s := r.m, r.m.Store
	pUni := (s.Uni[w] + m.P.KappaUni*m.LM.Uni(w)) / (s.Total + m.P.KappaUni)
	return kappa * (m.P.Mu*m.LM.Bi(left, w) + (1-m.P.Mu)*pUni)
}

// safe：w 的每個讀音下，w 與每個同音候選的分數區間（各自的 Bi 計數在或不在）都不重疊，
// 而且至少相隔 margin 倍。區間兩端都算，同一輪其他計數一起被清也不會改變先後。
// 詞庫查不到讀音的字面符號沒有同音候選，不影響選字；無法查讀音的詞庫一律保留。
func (r *rankChecker) safe(left, w string, count, kappa float64) bool {
	if r.lookup == nil {
		return false
	}
	seen := map[string]bool{}
	for _, e := range r.lookup.Lookup(w) {
		k := zhuyin.Key(e.Reading)
		if e.Correction || seen[k] {
			continue
		}
		seen[k] = true
		h := r.homophones(e.Reading)
		pw, ok := h[w]
		if !ok {
			continue
		}
		base := r.prior(left, w, kappa)
		lo, hi := base*pw, (base+count)*pw
		for x, px := range h {
			if x == w {
				continue
			}
			bx := r.prior(left, x, kappa)
			xlo, xhi := bx*px, (bx+r.m.Store.Bi[pair(left, x)])*px
			if !(lo > xhi*r.margin || hi*r.margin < xlo) {
				return false
			}
		}
	}
	return true
}
