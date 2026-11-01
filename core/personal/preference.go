package personal

import (
	"slices"
	"strings"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 讀音習慣（#218）：使用者反覆選用同一個多字詞時，它在同讀音的競爭裡得到一份有上限的分數，
// 沒打過的前文也看得到。有語境的更正只管同一組鄰音，個人 unigram 被系統 bigram 蓋過，
// 這份分數補上「不管前面是什麼，這個讀音我幾乎都打這個詞」。
//
// 證據要同時滿足：
//   - 手選過（Store.Confirmed）。被動提交不會建立紀錄：首選錯了沒改的詞不會自己變成習慣。
//   - 手選與使用的證據合起來過門檻。長期證據每日有上限，同一天選十二次、在十二個語境各選一次
//     都只算一天，一次選擇蓋不過任意語境。
//   - 比同讀音的其他詞常用（EvidenceByWord）。兩個詞都常打時交回語境判斷。
//
// 這是弱偏好，不是 CorrectionPenalty：只加分、有上限、會衰減。神經選字由 session.overturnsHabit 處理：
// 評分器沒有壓倒性把握時不換掉這個詞，有的話聽評分器；證據退到門檻以下就回到原本的規則。
const (
	// habitMinSyllables：單字的同音字隨語境而定（在／再），不算習慣。
	habitMinSyllables = 2
	// habitThreshold：同一天最多到 1.6（手選每日上限 1，加使用每日上限 1 的 0.6），一定要跨日才過；
	// 三天各選一次（約 2.7）會過，一次手選（0.75）要再加上大約三天的常用才過。工程初值。
	habitThreshold = 2.4
	// habitFull：證據到這裡拿到全部的 HabitLift。
	habitFull = 2.6
	// habitUseShare：使用證據折算成手選證據的比例。被動提交可能是沒注意到的錯字，只算一部分。
	habitUseShare = 0.6
	// 占同讀音使用證據的比例：habitShareLow 以下不加分，habitShareFull 以上全加。
	habitShareLow  = 0.5
	habitShareFull = 0.8
	// habitLeaderRatio：系統詞頻超過最強同音詞這個倍數的詞，本來就是首選，不需要習慣幫忙。
	habitLeaderRatio = 3.0
	// confirmUnit 是一次手選的證據，與 Correction.Usage 同單位（WManual−WPassive）。
	confirmUnit = 0.75
)

type habitLift struct {
	reading string
	lift    float64
}

// habitIndex 依詞面查分數，只在個人資料更新後（提交、每小時的衰減）重建，不是每鍵；
// 沒有手選紀錄的詞查一次 map 就結束，不必組讀音鍵。
type habitIndex struct {
	store *Store
	gen   int
	time  int64
	lifts map[string][]habitLift
}

func clamp01(x float64) float64 { return max(0, min(1, x)) }

// ReadingLift 是 word 以 reading 讀時的讀音習慣分數（自然對數）；0 表示沒有習慣（decode.ReadingPreference）。
func (m *Model) ReadingLift(word string, reading []zhuyin.Syllable) float64 {
	if m.P.HabitLift <= 0 || len(m.Store.Confirmed) == 0 {
		return 0
	}
	idx := &m.habitIndex
	if idx.store != m.Store || idx.gen != m.Gen || idx.time != m.Store.Time {
		m.buildHabitIndex()
	}
	lifts := idx.lifts[word]
	if lifts == nil {
		return 0
	}
	k := zhuyin.Key(reading)
	for _, l := range lifts {
		if l.reading == k {
			return l.lift
		}
	}
	return 0
}

func (m *Model) buildHabitIndex() {
	s := m.Store
	idx := &m.habitIndex
	*idx = habitIndex{store: s, gen: m.Gen, time: s.Time}
	for k, e := range s.Confirmed {
		word, reading, _ := strings.Cut(k, "\t")
		picked := e.at(s.Time).Long
		if picked <= 0 {
			continue
		}
		used := s.EvidenceByWord[word].at(s.Time).Long
		evidence := picked + habitUseShare*used
		if evidence < habitThreshold || used <= 0 {
			continue
		}
		rival, sysRival, found := m.rivalUse(word, reading)
		// 沒有同音詞要比，或這個詞本來就是系統的首選（不是／不適）：不必加分。它輸的語境是系統有根據的
		// （不適用），加了只會去搶斷詞、蓋過語境。
		if !found || m.LM.Uni(word) > habitLeaderRatio*sysRival {
			continue
		}
		lift := m.P.HabitLift * clamp01(evidence/habitFull) * clamp01((used/(used+rival)-habitShareLow)/(habitShareFull-habitShareLow))
		if lift <= 0 {
			continue
		}
		if idx.lifts == nil {
			idx.lifts = map[string][]habitLift{}
		}
		idx.lifts[word] = append(idx.lifts[word], habitLift{reading, lift})
	}
}

// rivalUse 加總同讀音其他詞的長期使用證據，並回傳其中最高的系統詞頻；found 回報有沒有其他詞。
func (m *Model) rivalUse(word, reading string) (rival, sysRival float64, found bool) {
	s := m.Store
	seen := []string{word}
	for _, e := range m.Lex.Exact(zhuyin.ParseReading(reading)) {
		if e.Correction || slices.Contains(seen, e.Surface) {
			continue
		}
		seen = append(seen, e.Surface)
		found = true
		rival += s.EvidenceByWord[e.Surface].at(s.Time).Long
		sysRival = max(sysRival, m.LM.Uni(e.Surface))
	}
	return rival, sysRival, found
}

// SplitConfirmed 回報 left 後面接 word 是否提交過：讀音習慣要切開完整詞（left 是某個完整詞的前綴）時，
// 必須使用者真的這樣打過，否則「過程是」會因為習慣打「程式」變成「過程式」。
func (m *Model) SplitConfirmed(left, word string) bool { return m.ObservedPair(left, word) }

// noteConfirmed 記下一次手選，同讀音其他詞的紀錄扣掉一次。與更正的矛盾處理相同：
// 一次反向選取不抹掉習慣，選的次數追上之後才讓位。
func (m *Model) noteConfirmed(word, reading string) {
	s := m.Store
	k := pair(word, reading)
	s.Confirmed[k] = s.Confirmed[k].add(s.Time, confirmUnit)
	for _, e := range m.Lex.Exact(zhuyin.ParseReading(reading)) {
		if e.Surface == word {
			continue
		}
		rk := pair(e.Surface, reading)
		if u, ok := s.Confirmed[rk]; ok {
			u = u.at(s.Time)
			u.Long = max(0, u.Long-confirmUnit)
			s.Confirmed[rk] = u
		}
	}
}

// keepConfirmed：照常打著的詞，手選紀錄不衰減。習慣生效後首選就對了，使用者不必再手選，
// 不這樣做的話證據會退到門檻以下，又要重新選一輪。只暫停衰減、不增加：被動提交不能累積出習慣。
func (m *Model) keepConfirmed(syls []zhuyin.Syllable, g Seg) {
	s := m.Store
	if len(s.Confirmed) == 0 || g.Partial || g.End-g.Start < habitMinSyllables || g.Start < 0 || g.End > len(syls) {
		return
	}
	k := pair(g.Word, zhuyin.Key(syls[g.Start:g.End]))
	if e, ok := s.Confirmed[k]; ok && s.Time > e.Updated {
		e.Updated = s.Time
		s.Confirmed[k] = e
	}
}

// seedConfirmed 從舊檔留下的更正推回手選紀錄（格式 5 以前）。逐筆當作當時選了一次，每日上限照樣算；
// 一般計數分不出手選與被動，不用來補造歷史，所以只會少算、不會多算。
func (s *Store) seedConfirmed() {
	keys := make([]string, 0, len(s.Corrections))
	for k, c := range s.Corrections {
		if n := len(zhuyin.ParseReading(c.Reading)); n >= habitMinSyllables && len([]rune(c.Word)) == n {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		if d := s.Corrections[a].Updated - s.Corrections[b].Updated; d != 0 {
			return int(max(-1, min(1, d)))
		}
		return strings.Compare(a, b)
	})
	for _, k := range keys {
		c := s.Corrections[k]
		key := pair(c.Word, c.Reading)
		s.Confirmed[key] = s.Confirmed[key].add(c.Updated, confirmUnit)
	}
}
