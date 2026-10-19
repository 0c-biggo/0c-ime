package personal

import (
	"slices"
	"strings"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// 自訂詞（#122）接進個人化模型的部分：詞庫視圖加入自訂詞（personal.go 的 view）、
// 沒有系統詞頻時的下限，以及修改自訂詞時同步學習資料。

// defaultVocabFloor 是語言模型不提供各字數分布時的下限。
const defaultVocabFloor = 1e-6

func (m *Model) vocabLen() int {
	if m.Vocab == nil {
		return 0
	}
	return m.Vocab.Len()
}

// Explicit 回報 w 是否為使用者親手加入的詞（decode.Explicit：補全時當作已有幾次觀察）。
func (m *Model) Explicit(w string) bool { return m.IsVocab(w) }

// HasExplicit 回報是否有任何自訂詞（decode.Explicit）。
func (m *Model) HasExplicit() bool { return m.vocabLen() > 0 }

// Learned 回報 w 有沒有自動學到的資料（候選窗反悔鍵據此決定能不能「忘記」）。
func (m *Model) Learned(w string) bool {
	_, phrase := m.Store.Phrases[w]
	return phrase || m.Store.Uni[w] > 0
}

// HasVocab 回報是否有任何自訂詞。
func (m *Model) HasVocab() bool { return m.vocabLen() > 0 }

// VocabReading 回報 w 是否為讀音 syls 的自訂詞。
func (m *Model) VocabReading(w string, syls []zhuyin.Syllable) bool {
	return m.vocabLen() > 0 && m.Vocab.HasReading(w, zhuyin.Key(syls))
}

// VocabEnglish 回報混打的待定按鍵 raw 是否恰好是使用者加的英文詞（大小寫照字面，#146）。
func (m *Model) VocabEnglish(raw string) bool {
	return m.Vocab != nil && m.Vocab.HasEnglish(raw)
}

// VocabEnglishPrefix 回報是否有英文詞以 raw 開頭（含恰好相等）。
func (m *Model) VocabEnglishPrefix(raw string) bool {
	return m.Vocab != nil && m.Vocab.HasEnglishPrefix(raw)
}

// IsVocab 回報 w 是否為自訂詞。
func (m *Model) IsVocab(w string) bool {
	if m.vocabLen() == 0 {
		return false
	}
	_, ok := m.Vocab.Lookup(w)
	return ok
}

// sysUni 是系統 unigram；自訂詞至少是同字數系統詞的第 90 百分位（lm.VocabQuantile），加了就打得到，
// 但常用的同音詞（例如「世界」之於自訂的「世傑」）照樣排前面，交給語境與選用紀錄決定。
func (m *Model) sysUni(w string) float64 {
	p := m.LM.Uni(w)
	if m.vocabLen() == 0 || !m.IsVocab(w) {
		return p
	}
	return max(p, m.vocabFloor(utf8.RuneCountInString(w)))
}

func (m *Model) vocabFloor(n int) float64 {
	med, ok := m.LM.(interface{ UniQuantileByLen() []float64 })
	if !ok {
		return defaultVocabFloor
	}
	table := med.UniQuantileByLen()
	for k := min(n, len(table)-1); k > 0; k-- { // 比系統最長的詞還長：用最長那一級
		if table[k] > lm.Unseen {
			return table[k]
		}
	}
	return defaultVocabFloor
}

// VocabAdd 加入自訂詞並讓解碼結果過期。親手加的詞解除先前「忘記」留下的 tombstone。
func (m *Model) VocabAdd(entries []vocab.Entry, source string) (*vocab.Batch, error) {
	if m.Vocab == nil {
		m.Vocab = vocab.New()
	}
	b, err := m.Vocab.Add(entries, source, m.now())
	m.vocabChanged(b)
	return b, err
}

// VocabRemove 刪除自訂詞。自動學到的部分不動：刪掉的詞回到一般學習規則。
func (m *Model) VocabRemove(texts []string, source string) (*vocab.Batch, error) {
	if m.Vocab == nil {
		m.Vocab = vocab.New()
	}
	b, err := m.Vocab.Remove(texts, source, m.now())
	m.vocabChanged(b)
	return b, err
}

// VocabSetReadings 改寫一個自訂詞的讀音。
func (m *Model) VocabSetReadings(text string, readings []string, source string) (*vocab.Batch, error) {
	if m.Vocab == nil {
		m.Vocab = vocab.New()
	}
	b, err := m.Vocab.SetReadings(text, readings, source, m.now())
	m.vocabChanged(b)
	return b, err
}

// VocabUndo 整批復原。
func (m *Model) VocabUndo(id uint64, source string) (*vocab.Batch, error) {
	if m.Vocab == nil {
		m.Vocab = vocab.New()
	}
	b, err := m.Vocab.Undo(id, source, m.now())
	m.vocabChanged(b)
	return b, err
}

func (m *Model) vocabChanged(b *vocab.Batch) {
	if b == nil {
		return
	}
	m.Gen++
	for _, c := range b.Changes {
		if w := pick(c.Before, c.After); w != nil && w.English() {
			continue // 英文詞只影響混打時照字面送出，與選字的學習資料無關（刪除時也不能忘記學到的英文字）
		}
		if c.After == nil {
			// 刪除自訂詞：連同加詞時記下的手選、更正與使用證據一起忘記，留下 tombstone，被動使用不會學回
			// （#122）；否則指向已不存在的詞的更正會讓同一組讀音解出奇怪的拆法。復原時會重新學回。
			if c.Before != nil {
				m.Forget(c.Before.Text)
			}
			continue
		}
		delete(m.Store.Tombstones, c.After.Text)
		// 親手加詞是對這組讀音最新的明確選擇。使用者以前對這組讀音選過別的寫法（例如手選提交過「士傑」）時，
		// 那些更正與使用證據會一直蓋過剛加的「世傑」：取代不一致的舊更正，並記成一次手選。沒有衝突時不另外學習，
		// 交給自訂詞本身的排序（第 90 百分位下限、第一頁、神經保護）——每個詞都記成手選會在句首累積大量證據，
		// 可能增加組字途中的回改。只處理新增的讀音；
		// 私人欄位與暫停學習時不會走到這裡（組字中加詞會先拒絕）。
		for _, rk := range c.After.Readings {
			if c.Before != nil && slices.Contains(c.Before.Readings, rk) {
				continue
			}
			syls := zhuyin.ParseReading(rk)
			seg := Seg{Word: c.After.Text, End: len(syls), Manual: true}
			if m.supersedeCorrections(syls, 0, len(syls), seg.Word) || m.choseOtherSpelling(c.After.Text, rk) ||
				m.committedOtherSpelling(c.After.Text, syls) {
				m.Learn(Commit{Syls: syls, Path: []Seg{seg}})
			}
		}
	}
}

func pick(a, b *vocab.Word) *vocab.Word {
	if a != nil {
		return a
	}
	return b
}

// choseOtherSpelling：使用者曾用讀音 rk 親手選過 w 以外的多字詞。
func (m *Model) choseOtherSpelling(w, rk string) bool {
	for k := range m.Store.Readings {
		if x, r, _ := strings.Cut(k, "\t"); r == rk && x != w {
			return true
		}
	}
	return false
}

// committedOtherSpelling：使用者曾把整組讀音 syls 打成 w 以外的寫法並提交過，即使沒有手選（例如照預設提交了
// 「士傑」）：路徑上每個詞都有使用紀錄、相鄰兩個詞有連用紀錄，接起來剛好是這組讀音。這種被動提交不留更正，
// 但句首等語境的連用證據會一直蓋過剛加的「世傑」。
// 只看確實接連提交過的整段，個別字在別處用過不算，避免沒有衝突時也記成手選。
func (m *Model) committedOtherSpelling(w string, syls []zhuyin.Syllable) bool {
	if m.Lex == nil || len(m.Store.Uni) == 0 {
		return false
	}
	// 結果只取決於位置、前一個詞，以及目前為止是否還跟 w 的開頭相同；記下走不通的狀態，長讀音也不會指數成長。
	type state struct {
		i      int
		prev   string
		prefix bool
	}
	dead := map[state]bool{}
	var walk func(i int, prev, text string) bool
	walk = func(i int, prev, text string) bool {
		if i == len(syls) {
			return text != w
		}
		st := state{i, prev, strings.HasPrefix(w, text)}
		if dead[st] {
			return false
		}
		seen := map[string]bool{}
		for j := i + 1; j <= len(syls); j++ {
			for _, e := range m.Lex.Exact(syls[i:j]) {
				x := e.Surface
				if seen[x] || m.Store.Uni[x] <= 0 || prev != "" && m.Store.Bi[pair(prev, x)] <= 0 {
					continue
				}
				seen[x] = true
				if walk(j, x, text+x) {
					return true
				}
			}
		}
		dead[st] = true
		return false
	}
	return walk(0, "", "")
}

func (m *Model) now() int64 {
	if m.Now == nil {
		return 0
	}
	return m.Now()
}
