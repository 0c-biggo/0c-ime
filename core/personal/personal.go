// Package personal 實作個人化模型：階層式貝氏調適、學習規則、
// 個人片語、個人讀音自適應、tombstone，以及自動修正的拒絕計數。
package personal

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/emoji"
	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Params 控制個人學習、補全與系統模型的混合權重。
type Params struct {
	KappaBi     float64 // 先驗強度下限，小：稀疏上下文一次觀察即有力
	KappaUni    float64 // 大：無上下文的偏好需要大量證據才改變，防止全域污染；須 ≥ 3e3
	WManual     float64 // 手動選字總證據；超過 WPassive 的部分只在相符語境使用
	WPassive    float64 // 被動接受首選的權重
	WCompletion float64 // 接受系統補全：介於主動選字與被動提交之間
	Mu          float64 // 系統 bigram 與 unigram 的內插
	// TypePrior 依「這個前文後面接過幾種不同的詞」加強系統先驗（Witten–Bell）：每種後接詞最多算
	// 一次被動提交的權重，乘上此倍率後與 KappaBi 取大。句首、「在」「沒有」這類常用前文接過的詞
	// 很分散，沒見過的後接詞不該被總次數壓到只剩系統機率的千分之一；只接過一兩種詞的稀疏前文
	// 不受影響。0 停用（等同只用 KappaBi）。
	TypePrior float64

	PhraseMaxLen     int // 片語晉升的長度上限（字）
	PhrasePromoteAt  int // 暫定片語出現幾次後晉升
	RejectsToDisable int // 同一項自動修正被拒絕幾次後對此使用者停用
}

func DefaultParams() Params {
	return Params{
		KappaBi: 1, TypePrior: 1, KappaUni: 10000, WManual: 1, WPassive: 0.25, WCompletion: 0.5, Mu: 0.7,
		PhraseMaxLen: 4, PhrasePromoteAt: 2, RejectsToDisable: 2,
	}
}

// StoreFormat 是 user.json 的格式版本。新增、移除欄位或改變既有欄位意義都要加一：
// 舊版讀檔會略過不認得的欄位、存檔時丟掉它們，只有版本號能讓舊版知道不該寫回。
// 0（未記錄）是加入版本號以前的格式，與 1 相同；2 加入 Pronouns；3 加入 English；
// 4 加入有上限且獨立於詞彙學習的 EmojiRecent；5 加入表情的常用分數 EmojiStats 與上次選用的
// = 選單分頁 PaletteTab，EmojiRecent 改為保存（含遲滯的）顯示順序，舊版照樣讀得懂（#124）。
const StoreFormat = 5

// NewerFormatError 表示個人資料由較新版本寫成；呼叫端不得以目前的結構寫回。
type NewerFormatError struct{ Format int }

func (e *NewerFormatError) Error() string {
	return fmt.Sprintf("個人資料格式 %d 比本版支援的 %d 新", e.Format, StoreFormat)
}

// Store 是可序列化的個人資料。鍵一律是文字，不用系統詞庫的整數 ID，詞庫改版不影響它。
// 不存完整句子，也不存未涉及學習的原始按鍵。
type Store struct {
	Format           int                   // 見 StoreFormat；存檔時一律寫入目前版本
	Corrections      map[string]Correction // 讀音區間＋左右鄰接音節 → 最近明確選取
	CorrectionSerial uint64
	Bi               map[string]float64 // "左詞\t詞" → 權重和
	Ctx              map[string]float64 // 左詞 → 權重和
	Uni              map[string]float64
	Total            float64
	// Uses 保留舊版提交統計以相容存檔；不再當作補全信心。
	Uses              map[string]float64
	EvidenceByContext map[string]UsageEvidence // "左詞\t詞" → 有來源、時間的證據
	EvidenceByWord    map[string]UsageEvidence // 只供弱全域回退
	Time              int64                    // 宿主提供的 Unix 秒；核心不自行讀取時鐘
	TimeSet           bool

	Picked      map[string][]string // 音節 → 曾手動選過的單字（供候選視窗）
	PhraseSeen  map[string]int
	Phrases     map[string]string // 詞面 → 讀音鍵
	PhraseOrder []string          // 晉升順序，確保查詢結果可重現
	Tombstones  map[string]bool
	Readings    map[string]bool // "詞\t讀音鍵"：使用者親手用這個讀音選過這個詞
	Rejects     map[string]int  // "詞\t讀音鍵"：自動修正被推翻的次數

	Pronouns    map[string]PronounHabit    // 中性代名詞（他、你）→ 使用者寫成「她」「妳」的習慣
	English     map[string]EnglishEvidence // 正規化英文詞 → 有界確認／使用證據；不存句子或按鍵
	EmojiRecent []string                   // = 選單的表情顯示順序，最多 64 項；不參與文字學習。
	EmojiStats  map[string]emoji.Stat      // EmojiRecent 各項的常用分數（衰減後次數）
	PaletteTab  string                     // 上次真的選了東西的 = 選單分頁：emoji、kaomoji 或 phrases
}

func NewStore() *Store {
	return &Store{
		Corrections: map[string]Correction{},
		Bi:          map[string]float64{}, Ctx: map[string]float64{}, Uni: map[string]float64{}, Uses: map[string]float64{},
		EvidenceByContext: map[string]UsageEvidence{}, EvidenceByWord: map[string]UsageEvidence{},
		Picked: map[string][]string{}, PhraseSeen: map[string]int{}, Phrases: map[string]string{},
		Tombstones: map[string]bool{}, Readings: map[string]bool{}, Rejects: map[string]int{},
		Pronouns: map[string]PronounHabit{},
		English:  map[string]EnglishEvidence{},
	}
}

// Clone 深度複製個人資料。宿主在引擎鎖內複製、鎖外序列化，存檔不必跟按鍵搶鎖。
func (s *Store) Clone() *Store {
	c := *s
	c.Corrections = maps.Clone(s.Corrections)
	c.Bi, c.Ctx, c.Uni, c.Uses = maps.Clone(s.Bi), maps.Clone(s.Ctx), maps.Clone(s.Uni), maps.Clone(s.Uses)
	c.EvidenceByContext, c.EvidenceByWord = maps.Clone(s.EvidenceByContext), maps.Clone(s.EvidenceByWord)
	c.Picked = make(map[string][]string, len(s.Picked))
	for k, v := range s.Picked {
		c.Picked[k] = slices.Clone(v)
	}
	c.PhraseSeen, c.Phrases, c.PhraseOrder = maps.Clone(s.PhraseSeen), maps.Clone(s.Phrases), slices.Clone(s.PhraseOrder)
	c.Tombstones, c.Readings, c.Rejects = maps.Clone(s.Tombstones), maps.Clone(s.Readings), maps.Clone(s.Rejects)
	c.Pronouns = make(map[string]PronounHabit, len(s.Pronouns))
	for k, v := range s.Pronouns {
		v.Cues = maps.Clone(v.Cues)
		c.Pronouns[k] = v
	}
	c.English = maps.Clone(s.English)
	c.EmojiRecent = slices.Clone(s.EmojiRecent)
	c.EmojiStats = maps.Clone(s.EmojiStats)
	return &c
}

func (s *Store) Save(w io.Writer) error {
	s.Format = StoreFormat
	return json.NewEncoder(w).Encode(s)
}

// LoadStore 讀回個人資料。較新版本寫成的檔案回傳 *NewerFormatError，不回傳可能誤讀的內容。
func LoadStore(r io.Reader) (*Store, error) {
	s := NewStore()
	if err := json.NewDecoder(r).Decode(s); err != nil {
		return nil, err
	}
	if s.Format > StoreFormat {
		return nil, &NewerFormatError{Format: s.Format}
	}
	s.cleanEnglish()
	s.EmojiRecent = emoji.CleanRecent(s.EmojiRecent)
	if s.EmojiStats == nil && len(s.EmojiRecent) > 0 { // 格式 4 以前：最近清單轉成初始分數，順序不變
		s.EmojiStats = emoji.SeedStats(s.EmojiRecent)
	}
	s.EmojiStats = emoji.CleanStats(s.EmojiStats, s.EmojiRecent)
	return s, nil
}

func pair(a, b string) string { return a + "\t" + b }

// Model 把系統語言模型與個人資料合成解碼用的機率，並負責學習。
type Model struct {
	englishIndex      *english.Dictionary
	englishIndexStore *Store
	englishIndexBase  *english.Dictionary
	englishIndexGen   int
	correctionIndex   correctionIndex
	diversityIndex    diversityIndex
	Store             *Store
	// Vocab 是使用者親手加入的自訂詞（#122）；nil 表示沒有。由宿主載入，修改走 VocabAdd 等方法。
	Vocab *vocab.Set
	LM    lm.System
	Lex   lexicon.Source
	P     Params
	// Gen 在個人資料每次改變時遞增，供 session 判斷解碼結果是否過期。
	Gen int
	// Now 由互動宿主注入；nil 時凍結時間，測試與 transcript 可確定性重播。
	Now func() int64
	// prunedDay 是這個行程最後一次清理的日子（PruneIfDue）；不存檔。
	prunedDay int64
	// undo 是 Undoable 執行期間記錄改動的複本；其餘時候是 nil（見 recording）。
	undo *Undo
}

func NewModel(lex lexicon.Source, sys lm.System) *Model {
	return &Model{Store: NewStore(), LM: sys, Lex: lex, P: DefaultParams()}
}

// Prob 回傳 P(w | left)：系統機率為先驗、個人計數為觀察的階層式貝氏調適。
// 每一層都是機率的凸組合，恆 ≤ 1，不會互相墊高。
func (m *Model) Prob(left, w string) float64 {
	s := m.Store
	pUni := (s.Uni[w] + m.P.KappaUni*m.sysUni(w)) / (s.Total + m.P.KappaUni)
	pSys := m.P.Mu*m.LM.Bi(left, w) + (1-m.P.Mu)*pUni
	ctx := s.Ctx[left]
	if ctx == 0 { // 這個上下文沒有任何個人觀察：c(L,w) 必為 0，省下組鍵
		return pSys
	}
	kappa := m.kappa(left)
	return (s.Bi[pair(left, w)] + kappa*pSys) / (ctx + kappa)
}

// SystemProb 是完全不含個人資料的系統機率（decode.Evidenced）。Prob 的先驗部分混有個人 unigram
// 計數，用過兩次的詞就會超過「麻煩」這類常用詞的系統機率，不能拿來當補全信心的先驗。
func (m *Model) SystemProb(left, w string) float64 {
	return m.P.Mu*m.LM.Bi(left, w) + (1-m.P.Mu)*m.sysUni(w)
}

// SystemBigram 是 SystemProb 中系統 bigram 實際觀察到的部分，不含退回單字頻率的內插（decode.SystemBigram）。
func (m *Model) SystemBigram(left, w string) float64 { return m.P.Mu * m.LM.Bi(left, w) }

// HasContext 回報 P(w | left) 是否可能因 left 而不同：系統語言模型有以 left 為前詞的 bigram，
// 或使用者在 left 之後有個人觀察。供解碼器合併無上下文的狀態（decode.Contextual）。
func (m *Model) HasContext(left string) bool {
	if m.Store.Ctx[left] > 0 {
		return true
	}
	if c, ok := m.LM.(interface{ HasLeft(string) bool }); ok {
		return c.HasLeft(left)
	}
	return true // 無法判斷時保守視為有上下文
}

// ObservedPair 回報使用者是否提交過 left 後面緊接 w（decode.PairObserved）。
func (m *Model) ObservedPair(left, w string) bool { return m.Store.Bi[pair(left, w)] > 0 }

// Prior 是不含上下文與個人化的系統先驗，供解碼器排序候選（不影響分數）。
func (m *Model) Prior(w string) float64 { return m.sysUni(w) }

func (m *Model) HasAdaptation() bool { return len(m.Store.Bi) > 0 || len(m.Store.Uni) > 0 }

// PriorMass removes positive learned counts while preserving the same context
// denominator as Prob. The decoder uses it only to cap proper word prefixes.
func (m *Model) PriorMass(left, word string) float64 {
	kappa := m.kappa(left)
	return m.SystemProb(left, word) * kappa / (m.Store.Ctx[left] + kappa)
}

// ChoseReading 回報使用者是否曾親手用讀音 syls 選過 w（個人讀音自適應的同一份紀錄）。
// 自訂詞的讀音視同親手選過：神經選字不得把它換成使用者沒選過的寫法（#96、#122）。
func (m *Model) ChoseReading(w string, syls []zhuyin.Syllable) bool {
	if len(m.Store.Readings) > 0 && m.Store.Readings[pair(w, zhuyin.Key(syls))] {
		return true
	}
	return m.Vocab != nil && m.Vocab.HasReading(w, zhuyin.Key(syls))
}

// PickedSingle 回報使用者是否曾以音節 syl 手動選過單字 w。
func (m *Model) PickedSingle(syl zhuyin.Syllable, w string) bool {
	for _, x := range m.Store.Picked[string(syl)] {
		if x == w {
			return true
		}
	}
	return false
}

// Source 回傳套用個人化之後的詞庫視圖：個人讀音覆寫 P(r|w)、停用被拒絕的自動修正、加入個人片語。
func (m *Model) Source() lexicon.Source { return view{m} }

type view struct{ m *Model }

func (v view) adjust(es []lexicon.Entry) []lexicon.Entry {
	if len(v.m.Store.Readings) == 0 && len(v.m.Store.Rejects) == 0 && v.m.vocabLen() == 0 {
		return es
	}
	out := make([]lexicon.Entry, 0, len(es))
	for _, e := range es {
		k := pair(e.Surface, zhuyin.Key(e.Reading))
		if e.Correction && v.m.Store.Rejects[k] >= v.m.P.RejectsToDisable {
			continue
		}
		if v.m.Store.Readings[k] || v.m.vocabLen() > 0 && !e.Correction && v.m.Vocab.HasReading(e.Surface, zhuyin.Key(e.Reading)) {
			e.PRW = 1
		}
		out = append(out, e)
	}
	return out
}

func (v view) phrases(match func(readingKey string) bool) []lexicon.Entry {
	if len(v.m.Store.PhraseOrder) == 0 {
		return nil
	}
	var out []lexicon.Entry
	for _, ph := range v.m.Store.PhraseOrder {
		if rk, ok := v.m.Store.Phrases[ph]; ok && match(rk) {
			out = append(out, lexicon.Entry{Surface: ph, Reading: zhuyin.ParseReading(rk), PRW: 1})
		}
	}
	return out
}

func hasSylPrefix(readingKey, prefixKey string) bool {
	return readingKey == prefixKey || strings.HasPrefix(readingKey, prefixKey+" ")
}

func (v view) Exact(syls []zhuyin.Syllable) []lexicon.Entry {
	es := v.adjust(v.sys().Exact(syls))
	if len(v.m.Store.PhraseOrder) > 0 {
		k := zhuyin.Key(syls)
		es = append(es, v.phrases(func(rk string) bool { return rk == k })...)
	}
	if v.m.vocabLen() > 0 {
		es = v.withVocab(es, v.m.Vocab.Exact(syls))
	}
	return es
}

func (v view) Prefixed(syls []zhuyin.Syllable) []lexicon.Entry {
	es := v.adjust(v.sys().Prefixed(syls))
	if len(v.m.Store.PhraseOrder) > 0 {
		k := zhuyin.Key(syls)
		es = append(es, v.phrases(func(rk string) bool { return hasSylPrefix(rk, k) })...)
	}
	if v.m.vocabLen() > 0 {
		es = v.withVocab(es, v.m.Vocab.Prefixed(syls, 0))
	}
	return es
}

// withVocab 加入自訂詞；系統詞庫或個人片語已有同詞面、同讀音的就不重複（adjust 已把它的讀音權重設為 1）。
func (v view) withVocab(es []lexicon.Entry, ms []vocab.Match) []lexicon.Entry {
	for _, m := range ms {
		dup := false
		for _, e := range es {
			if e.Surface == m.Text && !e.Correction && slices.Equal(e.Reading, m.Reading) {
				dup = true
				break
			}
		}
		if !dup {
			es = append(es, lexicon.Entry{Surface: m.Text, Reading: m.Reading, PRW: 1})
		}
	}
	return es
}

// System 與 Personal 讓解碼器把可快取的系統詞庫與會變動的個人部分分開查詢。
func (v view) System() lexicon.Source { return v.sys() }

// sys 是系統詞庫加上輕聲語氣詞退路（#69）；m.Lex 保持原本的詞庫。
func (v view) sys() lexicon.Source { return lexicon.NeutralParticles(v.m.Lex) }

// Adjust 對系統詞條套用個人讀音與修正拒絕。
func (v view) Adjust(es []lexicon.Entry) []lexicon.Entry { return v.adjust(es) }

// PersonalPrefixed 回傳讀音以 syls 開頭的個人片語。
func (v view) PersonalPrefixed(syls []zhuyin.Syllable) []lexicon.Entry {
	var out []lexicon.Entry
	if len(v.m.Store.PhraseOrder) > 0 {
		k := zhuyin.Key(syls)
		out = v.phrases(func(rk string) bool { return hasSylPrefix(rk, k) })
	}
	if v.m.vocabLen() > 0 {
		for _, m := range v.m.Vocab.Prefixed(syls, 0) {
			out = append(out, lexicon.Entry{Surface: m.Text, Reading: m.Reading, PRW: 1})
		}
	}
	return out
}

func (v view) HasPrefix(syls []zhuyin.Syllable) bool {
	if v.sys().HasPrefix(syls) || v.m.vocabLen() > 0 && v.m.Vocab.HasPrefix(syls) {
		return true
	}
	if len(v.m.Store.PhraseOrder) == 0 {
		return false
	}
	k := zhuyin.Key(syls)
	return len(v.phrases(func(rk string) bool { return hasSylPrefix(rk, k) })) > 0
}

// LongestWord 回報詞條讀音的最長音節數（decode.Long）：系統詞庫的長詞（#142）與自訂詞（#122）
// 都可以超過一般詞的解碼上限。
func (v view) LongestWord() int {
	n := lexicon.Longest(v.m.Lex)
	if v.m.vocabLen() > 0 {
		n = max(n, v.m.Vocab.Longest())
	}
	return n
}

func (v view) Has(surface string) bool {
	_, ok := v.m.Store.Phrases[surface]
	if ok || v.m.Lex.Has(surface) {
		return true
	}
	_, ok = v.vocabWord(surface)
	return ok
}

func (v view) vocabWord(surface string) (vocab.Word, bool) {
	if v.m.vocabLen() == 0 {
		return vocab.Word{}, false
	}
	return v.m.Vocab.Lookup(surface)
}

// Seg 是提交文字中的一段。
type Seg struct {
	Word       string
	Start, End int  // 音節位置，End 不含
	Manual     bool // 使用者手動選取（含接受補全，另以 Completion 區分）
	Completion bool // 接受系統提供的補全，不視同主動修正
	Suggested  bool // 接受神經次佳提示；提示中的代名詞性別由習慣決定，不算明確選取
	Partial    bool // 尾端未打完的詞的前綴；不是完整單位，不學習
	// Fuzzy 表示這一段經由模糊音（跨音）讀音到達。讀音不是使用者打的音，所以整段不學習：
	// 不記個人讀音、單字手選、片語，也不計詞頻，避免近音變成永久入口或自我強化（issue #45）。
	Fuzzy bool
}

// Commit 是一次提交的學習事件。
type Commit struct {
	Syls []zhuyin.Syllable
	Path []Seg
	// Rejected 是被使用者手動換掉的原區段，連同它當時的讀音是否為自動修正。
	Rejected []RejectedSeg
	// Selected 是被 Path 中較長的詞包住的手選區間（在「為甚麼」裡選「什麼」，顯示成一個詞「為什麼」；
	// 在「甚麼」裡只選「什」）。一般計數仍依 Path；手選本身照樣記成有語境更正與個人讀音（#96）。
	Selected []Seg
}

type RejectedSeg struct {
	Word       string
	Start, End int
	Correction bool
	// Reading 是被推翻的修正詞條自己的讀音；空的時候用 Commit.Syls 的那段（打聲調時兩者相同）。
	// 聲調選填（#150）時 Syls 是最後路徑上換上去的詞的讀音，拒絕次數要記在修正詞條的讀音上才停用得了。
	Reading []zhuyin.Syllable
}

func (m *Model) observe(left, w string, weight float64) {
	s := m.Store
	k := pair(left, w)
	before := s.Bi[k]
	valid := m.diversityValid()
	s.Bi[k] += weight
	s.Ctx[left] += weight
	s.Uni[w] += weight
	s.Total += weight
	s.Uses[w]++
	if valid {
		m.changeBi(left, before, s.Bi[k])
	}
}

func (m *Model) dropBi(left, w string) {
	k := pair(left, w)
	before := m.Store.Bi[k]
	valid := m.diversityValid()
	r := m.recording()
	noteFloat(r.bi, m.Store.Bi, k)
	noteFloat(r.ctx, m.Store.Ctx, left)
	m.Store.Ctx[left] -= before
	delete(m.Store.Bi, k)
	if valid {
		m.changeBi(left, before, 0)
	}
}

func runeLen(s string) int { return len([]rune(s)) }

// Learn 依學習規則更新個人資料。只在提交後呼叫。
func (m *Model) Learn(c Commit) {
	m.Gen++
	s := m.Store
	c.Path = filter(c.Path, func(g Seg) bool { return !g.Fuzzy })
	m.learnCorrections(c)
	m.learnPronouns(c)
	path := append([]Seg(nil), c.Path...)

	// 推翻自動修正：只計拒絕次數，該範圍的文字不進入一般學習。
	// 故意打的錯字是例外而非偏好，不該影響之後的選字。
	// 讀音還帶「沒打聲調」記號（#150，沒能換回詞庫讀音）時不計次數，範圍照樣不學。
	var noLearn [][2]int
	for _, r := range c.Rejected {
		if r.Correction {
			reading := c.Syls[r.Start:r.End]
			if len(r.Reading) == r.End-r.Start {
				reading = r.Reading
			}
			if !zhuyin.HasUntoned(reading) {
				s.Rejects[pair(r.Word, zhuyin.Key(reading))]++
			}
			noLearn = append(noLearn, [2]int{r.Start, r.End})
		}
	}
	path = filter(path, func(g Seg) bool {
		for _, r := range noLearn {
			if r[0] < g.End && g.Start < r[1] {
				return false
			}
		}
		return true
	})

	// 正規化：相鄰詞若合起來是已晉升的個人片語，就以片語為單位學習，
	// 組成字的全域計數不再因它成長（避免外溢污染）。
	var merged []Seg
	for _, g := range path {
		if n := len(merged); n > 0 {
			if _, ok := s.Phrases[merged[n-1].Word+g.Word]; ok && merged[n-1].End == g.Start {
				explicit := merged[n-1].Manual && !merged[n-1].Completion || g.Manual && !g.Completion
				merged[n-1].Word += g.Word
				merged[n-1].End = g.End
				merged[n-1].Manual = merged[n-1].Manual || g.Manual
				merged[n-1].Completion = (merged[n-1].Completion || g.Completion) && !explicit
				continue
			}
		}
		merged = append(merged, g)
	}
	path = filter(merged, func(g Seg) bool { return !g.Partial })

	left := func(k int) string {
		if k == 0 || path[k-1].End != path[k].Start {
			return lm.BOS
		}
		return path[k-1].Word
	}

	// 更正由條件偏好處理；不清除共用左鄰計數或全域補全證據。

	for _, g := range c.Selected {
		if !g.Fuzzy && !slices.ContainsFunc(noLearn, func(r [2]int) bool { return r[0] < g.End && g.Start < r[1] }) {
			m.noteManual(c.Syls, g)
		}
	}
	for k, g := range path {
		if g.Manual {
			m.noteManual(c.Syls, g)
		}
		if s.Tombstones[g.Word] {
			continue
		}
		w := m.P.WPassive
		if g.Manual {
			w = m.P.WPassive // 強手選證據已另存於相符讀音及鄰接範圍
			if g.Completion {
				w = m.P.WCompletion
			}
		}
		l := left(k)
		m.observe(l, g.Word, w)
		m.observeEvidence(l, g.Word, w)
		// 片語晉升：手動選取的區間與左鄰合併
		if g.Manual && l != lm.BOS && runeLen(l)+runeLen(g.Word) <= m.P.PhraseMaxLen {
			m.seePhrase(l+g.Word, zhuyin.Key(c.Syls[path[k-1].Start:g.End]), left(k-1), w)
		}
	}
	// 既有的暫定片語再次完整出現時累計
	for k := 1; k < len(path); k++ {
		if path[k-1].End != path[k].Start || path[k].Manual {
			continue
		}
		ph := path[k-1].Word + path[k].Word
		if _, seen := s.PhraseSeen[ph]; seen {
			m.seePhrase(ph, zhuyin.Key(c.Syls[path[k-1].Start:path[k].End]), left(k-1), m.P.WPassive)
		}
	}
}

// noteManual 記下明確手選：解除 tombstone、候選視窗的單字手選與個人讀音自適應。
// 讀音帶「沒打聲調」記號（#150）時什麼都不記：呼叫端應已標成 Fuzzy，這裡是最後一道防線。
func (m *Model) noteManual(syls []zhuyin.Syllable, g Seg) {
	if zhuyin.HasUntoned(syls[g.Start:g.End]) {
		return
	}
	s := m.Store
	delete(s.Tombstones, g.Word) // 明確的手動選取可解除 tombstone
	if g.End-g.Start == 1 && !m.PickedSingle(syls[g.Start], g.Word) {
		syl := string(syls[g.Start])
		s.Picked[syl] = append(s.Picked[syl], g.Word)
	}
	s.Readings[pair(g.Word, zhuyin.Key(syls[g.Start:g.End]))] = true // 個人讀音自適應
}

func (m *Model) seePhrase(ph, readingKey, left string, weight float64) {
	s := m.Store
	for t := range s.Tombstones {
		if strings.Contains(ph, t) {
			return
		}
	}
	s.PhraseSeen[ph]++
	m.observeEvidence(left, ph, weight)
	if _, have := s.Phrases[ph]; s.PhraseSeen[ph] >= m.P.PhrasePromoteAt && !have && !m.Lex.Has(ph) && !m.IsVocab(ph) {
		s.Phrases[ph] = readingKey
		s.PhraseOrder = append(s.PhraseOrder, ph)
		// 晉升時把已累積的證據交給片語，之後由片語這個單位接手計數。
		n := float64(s.PhraseSeen[ph])
		s.Uni[ph] += n
		s.Total += n
		s.Uses[ph] += n
	}
}

// Forget 是「不再學」：撤銷與 w 有關的一切學習並留下 tombstone，被動使用不得學回，
// 只有再次明確手選、親手加詞或「恢復學習」（Relearn）才解除。
func (m *Model) Forget(w string) { m.ForgetAll([]string{w}) }

// ForgetAll 等於對 ws 逐一 Forget，但每類學習資料只掃一遍（「靈犀學到的」頁勾選多個詞，#182）。
func (m *Model) ForgetAll(ws []string) {
	m.UnlearnAll(ws)
	r := m.recording()
	for _, w := range ws {
		m.tombstoneEnglish(w)
		note(r.tombstones, m.Store.Tombstones, w)
		m.Store.Tombstones[w] = true
	}
}

// Unlearn 是「移除」（#182）：撤銷與 w 有關的一切學習，但不留 tombstone；之後照常打字、選字會再學回來。
func (m *Model) Unlearn(w string) { m.UnlearnAll([]string{w}) }

// UnlearnAll 等於對 ws 逐一 Unlearn：清掉的是「含有任何一個詞」的項目，與逐一清的聯集相同，
// 但每類資料只掃一遍。個人資料到上限時（詞對 20 萬筆）掃一遍約 10 ms；逐詞各掃一遍的話，勾 100 個詞
// 要 0.6 秒、1,000 個詞要 6 秒，期間引擎鎖住、打字會卡（#182）。
func (m *Model) UnlearnAll(ws []string) {
	if len(ws) == 0 {
		return
	}
	m.Gen++
	has := containsAny(ws)
	word := make(map[string]bool, len(ws))
	for _, w := range ws {
		word[w] = true
	}
	isWord := func(x string) bool { return word[x] }
	r := m.recording()
	for _, w := range ws {
		m.forgetEnglish(w)
		m.forgetPronouns(w)
	}
	m.forgetCorrections(has)
	s := m.Store
	// 「左詞\t詞」的鍵：先看整個鍵（左詞或詞含有某個詞，整個鍵一定也含有），絕大多數不相干的鍵不必切開。
	for k := range s.EvidenceByContext {
		if !has(k) {
			continue
		}
		if l, x, _ := strings.Cut(k, "\t"); has(l) || has(x) {
			note(r.byContext, s.EvidenceByContext, k)
			delete(s.EvidenceByContext, k)
			note(r.byWord, s.EvidenceByWord, x)
			delete(s.EvidenceByWord, x) // 回退聚合無法拆回各上下文；避免撤銷後仍有殘留加權
		}
	}
	for x := range s.EvidenceByWord {
		if has(x) {
			note(r.byWord, s.EvidenceByWord, x)
			delete(s.EvidenceByWord, x)
		}
	}
	for k := range s.Bi {
		if !has(k) {
			continue
		}
		if l, x, _ := strings.Cut(k, "\t"); has(l) || has(x) {
			m.dropBi(l, x)
		}
	}
	dropUni := func(w string) {
		noteFloat(r.uni, s.Uni, w)
		noteFloat(r.uses, s.Uses, w)
		s.Total -= s.Uni[w]
		delete(s.Uni, w)
		delete(s.Uses, w)
	}
	for _, w := range ws {
		dropUni(w)
	}
	for syl, xs := range s.Picked {
		if slices.ContainsFunc(xs, isWord) {
			note(r.picked, s.Picked, syl)
			s.Picked[syl] = filter(xs, func(x string) bool { return !word[x] })
		}
	}
	for k := range s.Readings {
		if !has(k) {
			continue
		}
		if x, _, _ := strings.Cut(k, "\t"); has(x) {
			note(r.readings, s.Readings, k)
			delete(s.Readings, k)
		}
	}
	for ph := range s.Phrases {
		if has(ph) {
			note(r.phrases, s.Phrases, ph)
			delete(s.Phrases, ph)
			dropUni(ph)
		}
	}
	s.PhraseOrder = filter(s.PhraseOrder, func(ph string) bool { _, ok := s.Phrases[ph]; return ok })
	for ph := range s.PhraseSeen {
		if has(ph) {
			note(r.phraseSeen, s.PhraseSeen, ph)
			delete(s.PhraseSeen, ph)
		}
	}
}

// containsAny 回報字串是否含有 ws 任何一個（strings.Contains 的聯集）。詞少時逐一比對；
// 詞多時改成查表：每個字元位置先看是不是某個詞的第一個字（位元表），是的話才查從這裡開始、
// 長度與某個詞相同的子字串，成本跟詞數幾乎無關。
func containsAny(ws []string) func(string) bool {
	if len(ws) <= containsAnyLoop || slices.ContainsFunc(ws, func(w string) bool { return !utf8.ValidString(w) }) {
		// 不是合法 UTF-8 的詞可能從字元中間開始比對到，查表只看字元開頭，所以照逐一比對。
		return func(s string) bool {
			for _, w := range ws {
				if strings.Contains(s, w) {
					return true
				}
			}
			return false
		}
	}
	set := make(map[string]bool, len(ws))
	var lens []int
	var first [0x10000 / 64]uint64 // 基本多文種平面的首字；其他字元一律查表
	for _, w := range ws {
		if w == "" {
			return func(string) bool { return true } // strings.Contains(s, "") 恆為真
		}
		set[w] = true
		if !slices.Contains(lens, len(w)) {
			lens = append(lens, len(w))
		}
		if c, _ := utf8.DecodeRuneInString(w); c < 0x10000 {
			first[c/64] |= 1 << (c % 64)
		}
	}
	return func(s string) bool {
		for i, c := range s {
			if c < 0x10000 && first[c/64]&(1<<(c%64)) == 0 {
				continue
			}
			for _, n := range lens {
				if i+n <= len(s) && set[s[i:i+n]] {
					return true
				}
			}
		}
		return false
	}
}

// containsAnyLoop：詞數不超過這個值時逐一 strings.Contains 比較快（BenchmarkContainsAny）。
const containsAnyLoop = 4

func filter[T any](xs []T, keep func(T) bool) []T {
	out := xs[:0:0]
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}
