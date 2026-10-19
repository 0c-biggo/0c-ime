package personal

import "math"

// 初值是可驗證的工程假設，並非研究認定的最佳天數／次數。
const (
	daySeconds    = 24 * 60 * 60
	shortHalfLife = 2 * daySeconds
	longHalfLife  = 30 * daySeconds
	shortCap      = 8.0
	longCap       = 16.0
)

// UsageEvidence 同時保存近期重複與跨日習慣。長期證據每 UTC 日至多增加 1，
// 避免單次話題的大量重複被當成長期偏好。不保存句子或逐次使用歷史。
type UsageEvidence struct {
	Short, Long float64
	Updated     int64
	Day         int64
	DayWeight   float64
}

func decay(value float64, elapsed, halfLife int64) float64 {
	return value * math.Exp2(-float64(max(0, elapsed))/float64(halfLife))
}

func (e UsageEvidence) at(now int64) UsageEvidence {
	elapsed := max(0, now-e.Updated)
	e.Short = decay(e.Short, elapsed, shortHalfLife)
	e.Long = decay(e.Long, elapsed, longHalfLife)
	e.Updated = max(e.Updated, now)
	if day := now / daySeconds; day > e.Day {
		e.Day, e.DayWeight = day, 0
	}
	return e
}

func (e UsageEvidence) add(now int64, weight float64) UsageEvidence {
	e = e.at(now)
	weight = max(0, weight)
	e.Short = min(shortCap, e.Short+weight)
	longWeight := min(weight, max(0, 1-e.DayWeight))
	e.Long = min(longCap, e.Long+longWeight)
	e.DayWeight += longWeight
	return e
}

// SyncTime 每次互動開始時由 session 呼叫；一次請求內所有候選使用同一時間。
func (m *Model) SyncTime() {
	if m.Now != nil {
		m.AdvanceTime(m.Now())
	}
}

// AdvanceTime 注入時間；倒退的時鐘不增加權重。排序計數每小時至多整批衰減一次，
// 避免每鍵掃描個人詞庫。衰減到不影響排序的計數與證據由 Prune 另外清掉（宿主存檔前每日一次）；
// 個人片語、個人讀音、手選紀錄與更正不因衰減而刪除。
func (m *Model) AdvanceTime(now int64) {
	s := m.Store
	now = now / 3600 * 3600
	if !s.TimeSet {
		s.Time, s.TimeSet = now, true
		return
	}
	if now-s.Time < 3600 {
		return
	}
	factor := decay(1, now-s.Time, longHalfLife)
	for k, v := range s.Bi {
		s.Bi[k] = v * factor
	}
	for k, v := range s.Ctx {
		s.Ctx[k] = v * factor
	}
	for k, v := range s.Uni {
		s.Uni[k] = v * factor
	}
	s.Total *= factor
	s.Time = now
	m.Gen++
}

func (m *Model) observeEvidence(left, w string, weight float64) {
	s := m.Store
	k := pair(left, w)
	s.EvidenceByContext[k] = s.EvidenceByContext[k].add(s.Time, weight)
	s.EvidenceByWord[w] = s.EvidenceByWord[w].add(s.Time, weight)
}

// Evidence 是同一上下文的近期＋長期證據，加上其他上下文的弱長期回退。
// 回退扣掉已計入的本地長期證據，避免重複加權；上限 2 不足以單獨讓稀有詞
// 跨過目前 κ=5、MinConf=0.4 的補全門檻。
func (m *Model) Evidence(left, w string) float64 {
	s := m.Store
	local := s.EvidenceByContext[pair(left, w)].at(s.Time)
	global := s.EvidenceByWord[w].at(s.Time)
	return local.Short + local.Long + min(2, 0.25*max(0, global.Long-local.Long))
}

// WordEvidence 回傳字詞在各上下文累積、已依時間衰減的使用證據，供候選顯示個人常用標示。
// 不使用舊 Uses 推測習慣，也不修改存檔或排序權重。
func (m *Model) WordEvidence(w string) float64 {
	e, ok := m.Store.EvidenceByWord[w]
	if !ok {
		return 0
	}
	e = e.at(m.Store.Time)
	return e.Short + e.Long
}
