// Package vocab 是使用者親手加入的詞彙（自訂詞）與「我的片語」（#122、#124）。
//
// 自訂詞和自動學到的個人資料（core/personal）分開：存在 vocab.json，不衰減、不被容量逐出，
// 只有使用者（或使用者授權的工具）明確增刪。每次修改記成一個批次，最近 KeepBatches 批可以整批復原。
// 這個套件是純邏輯：時間由呼叫端傳入，讀寫檔案由宿主負責。
package vocab

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/zhuyin"
)

// Format 是 vocab.json 的格式版本，與 user.json 的 personal.StoreFormat 各自獨立。
// 新增、移除欄位或改變既有欄位意義都要加一；舊版讀到較新格式時不讀入也不寫回。
//
// 英文詞（#122 後續、#146）沒有加一：它和漢字詞一樣存在 words，只是詞面是英文、readings 是空的。
// 不認得英文詞的版本把它當成「手動改壞」的詞：不使用、原樣保留到下次存檔（見 reindex），
// 其餘詞照常讀寫，英文詞不會因為舊版寫回而消失。加一的話舊版整檔不讀，連漢字詞也打不出來。
const Format = 1

// 上限採固定常數，不提供使用者設定。
const (
	MaxWordRunes   = 32     // 每個詞 1～32 字，讀音音節數相同
	MaxEnglishLen  = 32     // 英文詞 1～32 個字元（ASCII 字母、數字與 - _ .）
	MaxWords       = 10_000 // 自訂詞總數
	MaxReadings    = 4      // 同一個詞的讀音數
	MaxPhrases     = 200    // 「我的片語」數量
	MaxPhraseRunes = 1_000  // 每則片語長度
	MaxBatchItems  = 500    // 單次修改的筆數
	KeepBatches    = 50     // 可整批復原的最近批次
)

// 來源。
const (
	SourceManual    = "manual"    // 設定程式「我的詞彙」或命令列
	SourceCompose   = "compose"   // 組字中選範圍加入
	SourceAssistant = "assistant" // 本機 API／MCP（使用者用的 AI 工具）
	SourceLearned   = "learned"   // 自動學到的片語轉成自訂詞
)

// 錯誤代碼，供 API 與設定程式判斷。
const (
	CodeInvalidWord    = "invalid_word"    // 空字串、含非漢字（也不是英文詞）、超過 MaxWordRunes／MaxEnglishLen
	CodeInvalidReading = "invalid_reading" // 音節不存在、音節數與字數不同
	CodeLimitWords     = "limit_words"
	CodeLimitReadings  = "limit_readings"
	CodeLimitPhrases   = "limit_phrases"
	CodeLimitBatch     = "limit_batch"
	CodeOverLimit      = "over_limit" // 檔案已超過上限（手動編輯）：不再新增
	CodeNotFound       = "not_found"
	CodeConflict       = "conflict" // 復原時資料已被後來的修改改過
	CodeEmpty          = "empty"
)

// Error 是修改被拒絕的原因。整批拒絕，不做部分寫入。
type Error struct {
	Code  string `json:"code"`
	Item  int    `json:"item"` // 批次中第幾筆（從 0 起）；與單筆無關時為 -1
	Text  string `json:"text,omitempty"`
	Msg   string `json:"message"`
	Limit int    `json:"limit,omitempty"`
	Left  int    `json:"left,omitempty"` // 還能加幾筆
}

func (e *Error) Error() string { return e.Msg }

func fail(code string, item int, text, format string, args ...any) *Error {
	return &Error{Code: code, Item: item, Text: text, Msg: fmt.Sprintf(format, args...)}
}

// NewerFormatError 表示檔案由較新版本寫成；呼叫端不得以目前的結構寫回。
type NewerFormatError struct{ Format int }

func (e *NewerFormatError) Error() string {
	return fmt.Sprintf("自訂詞彙格式 %d 比本版支援的 %d 新", e.Format, Format)
}

// Word 是一個自訂詞。Readings 是以空白分隔音節的讀音（zhuyin.Key），依加入順序。
// 英文詞（IsEnglish）沒有讀音，Readings 是空的。
type Word struct {
	Text     string   `json:"word"`
	Readings []string `json:"readings"`
	Source   string   `json:"source,omitempty"`
	Added    int64    `json:"added,omitempty"` // Unix 秒
	Batch    uint64   `json:"batch,omitempty"` // 最後修改它的批次
}

func (w Word) clone() Word {
	w.Readings = slices.Clone(w.Readings)
	if w.Readings == nil {
		w.Readings = []string{} // 英文詞：JSON 寫成 []，管理頁與 API 不必處理 null
	}
	return w
}

// English 回報這是英文詞（沒有讀音、混打時照字面送出）。
func (w Word) English() bool { return IsEnglish(w.Text) }

// IsEnglish 回報 text 是否為英文詞的詞面：1～MaxEnglishLen 個 ASCII 字母、數字與 - _ .，至少有一個字母。
// 大小寫照字面：ls 和 LS 是兩個不同的詞，混打時也照字面比對。
func IsEnglish(text string) bool {
	if text == "" || len(text) > MaxEnglishLen {
		return false
	}
	letter := false
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			letter = true
		case c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return letter
}

// englishLike：只有 ASCII 可見字元，看起來是想加英文詞（給錯誤訊息用）。
func englishLike(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] <= ' ' || text[i] > '~' {
			return false
		}
	}
	return text != ""
}

// Phrase 是「我的片語」的一則：沒有讀音、不參與選字，從 = 選單選取。
type Phrase struct {
	ID    uint64 `json:"id"`
	Text  string `json:"text"`
	Added int64  `json:"added,omitempty"`
	Batch uint64 `json:"batch,omitempty"`
}

// Change 是批次中的一筆變更。Before 為 nil 表示新增，After 為 nil 表示刪除；
// 片語用 PhraseBefore／PhraseAfter，同樣的規則。
type Change struct {
	Before       *Word   `json:"before,omitempty"`
	After        *Word   `json:"after,omitempty"`
	PhraseBefore *Phrase `json:"phrase_before,omitempty"`
	PhraseAfter  *Phrase `json:"phrase_after,omitempty"`
}

// Batch 是一次修改。
type Batch struct {
	ID      uint64   `json:"id"`
	Time    int64    `json:"time"`
	Source  string   `json:"source"`
	Op      string   `json:"op"` // add、remove、readings、undo、phrase-add、phrase-remove、phrase-move
	UndoOf  uint64   `json:"undo_of,omitempty"`
	Undone  bool     `json:"undone,omitempty"` // 已被後來的批次復原
	Changes []Change `json:"changes"`
}

type file struct {
	Format  int      `json:"format"`
	Version uint64   `json:"version"`
	Words   []Word   `json:"words"`
	Phrases []Phrase `json:"phrases"`
	Batches []Batch  `json:"batches"`
}

// Set 是載入記憶體的自訂詞彙與讀音索引。零值不可用，請用 New 或 Load。
// 不是並行安全的：由引擎在自己的鎖內使用。
type Set struct {
	f        file
	index    node
	byText   map[string]int  // 詞 → f.Words 的位置（只含有效、在上限內的詞，含英文詞）
	english  map[string]bool // 有效的英文詞（沒有讀音，不進讀音前綴樹）
	problems []string
	longest  int // 有效詞的最長音節數
}

// New 回傳空的詞彙。
func New() *Set {
	s := &Set{f: file{Format: Format}}
	s.reindex()
	return s
}

// Load 讀回 vocab.json。較新版本寫成的檔案回傳 *NewerFormatError。
// 內容有問題（手動改壞、超過上限）時照樣載入：只使用上限內、有效的詞，其餘原樣保留到下次存檔。
func Load(r io.Reader) (*Set, error) {
	s := &Set{}
	if err := json.NewDecoder(r).Decode(&s.f); err != nil {
		return nil, err
	}
	if s.f.Format > Format {
		return nil, &NewerFormatError{Format: s.f.Format}
	}
	s.f.Format = Format
	s.reindex()
	return s, nil
}

// Save 寫出整份詞彙。
func (s *Set) Save(w io.Writer) error {
	s.f.Format = Format
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(s.f)
}

// Clone 深度複製，給宿主在鎖內複製、鎖外存檔。
func (s *Set) Clone() *Set {
	c := &Set{f: file{Format: s.f.Format, Version: s.f.Version}}
	c.f.Words = make([]Word, len(s.f.Words))
	for i, w := range s.f.Words {
		c.f.Words[i] = w.clone()
	}
	c.f.Phrases = slices.Clone(s.f.Phrases)
	c.f.Batches = make([]Batch, len(s.f.Batches))
	for i, b := range s.f.Batches {
		b.Changes = slices.Clone(b.Changes)
		c.f.Batches[i] = b // Change 內的指標指向不再修改的快照，可以共用
	}
	c.reindex()
	return c
}

// Version 在每次修改時加一，給管理頁判斷是否需要重新整理。
func (s *Set) Version() uint64 { return s.f.Version }

// Len 回傳有效、在上限內的詞數。
func (s *Set) Len() int { return len(s.byText) }

// Stored 回傳檔案中的詞數（含無效或超過上限而沒有使用的）。
func (s *Set) Stored() int { return len(s.f.Words) }

// Problems 說明載入時沒有使用的內容，給記錄與管理頁說明。
func (s *Set) Problems() []string { return slices.Clone(s.problems) }

// Longest 回傳有效詞的最長音節數；解碼器據此決定要不要找超過系統詞長度的詞。
func (s *Set) Longest() int { return s.longest }

// Words 依檔案順序回傳有效的詞（複本）。
func (s *Set) Words() []Word {
	out := make([]Word, 0, len(s.byText))
	for i, w := range s.f.Words {
		if k, ok := s.byText[w.Text]; ok && k == i {
			out = append(out, w.clone())
		}
	}
	return out
}

// Lookup 回傳某個詞。
func (s *Set) Lookup(text string) (Word, bool) {
	i, ok := s.byText[text]
	if !ok {
		return Word{}, false
	}
	return s.f.Words[i].clone(), true
}

// HasReading 回報 text 是否為讀音 key 的自訂詞。
func (s *Set) HasReading(text, key string) bool {
	i, ok := s.byText[text]
	return ok && slices.Contains(s.f.Words[i].Readings, key)
}

// Phrases 依使用者排的順序回傳「我的片語」。
func (s *Set) Phrases() []Phrase { return slices.Clone(s.f.Phrases) }

// Batches 回傳保留的批次，最新的在前。
func (s *Set) Batches() []Batch {
	out := slices.Clone(s.f.Batches)
	slices.Reverse(out)
	return out
}

// Batch 回傳指定的批次。
func (s *Set) Batch(id uint64) (Batch, bool) {
	for _, b := range s.f.Batches {
		if b.ID == id {
			return b, true
		}
	}
	return Batch{}, false
}

// Normalize 檢查並整理一個詞與它的讀音：讀音的音節以空白分隔、去掉多餘空白，重複的讀音只留一個。
// 英文詞（IsEnglish）不能有讀音。item 是批次中的位置，用在錯誤訊息。
func Normalize(text string, readings []string, item int) (Word, *Error) {
	text = strings.TrimSpace(text)
	n := utf8.RuneCountInString(text)
	if n == 0 {
		return Word{}, fail(CodeInvalidWord, item, text, "第 %d 筆是空的", item+1)
	}
	if englishLike(text) {
		return normalizeEnglish(text, readings, item)
	}
	if n > MaxWordRunes {
		e := fail(CodeInvalidWord, item, text, "「%s」有 %d 個字，自訂詞最多 %d 字；更長的內容請放「我的片語」", text, n, MaxWordRunes)
		e.Limit = MaxWordRunes
		return Word{}, e
	}
	for _, r := range text {
		if !zhuyin.IsHan(r) {
			return Word{}, fail(CodeInvalidWord, item, text, "「%s」含有「%c」；自訂詞只收漢字，或由英文字母、數字與 - _ . 組成的英文詞，其他文字請放「我的片語」", text, r)
		}
	}
	if len(readings) == 0 {
		return Word{}, fail(CodeInvalidReading, item, text, "「%s」沒有讀音", text)
	}
	w := Word{Text: text}
	for _, raw := range readings {
		syls := zhuyin.ParseReading(raw)
		if len(syls) != n {
			return Word{}, fail(CodeInvalidReading, item, text, "「%s」有 %d 個字，讀音「%s」卻有 %d 個音節", text, n, strings.TrimSpace(raw), len(syls))
		}
		for _, syl := range syls {
			if !syl.Legal() {
				return Word{}, fail(CodeInvalidReading, item, text, "「%s」的讀音「%s」中「%s」不是注音音節", text, strings.TrimSpace(raw), syl)
			}
		}
		if k := zhuyin.Key(syls); !slices.Contains(w.Readings, k) {
			w.Readings = append(w.Readings, k)
		}
	}
	if len(w.Readings) > MaxReadings {
		e := fail(CodeLimitReadings, item, text, "「%s」有 %d 個讀音，同一個詞最多 %d 個", text, len(w.Readings), MaxReadings)
		e.Limit = MaxReadings
		return Word{}, e
	}
	return w, nil
}

func normalizeEnglish(text string, readings []string, item int) (Word, *Error) {
	if len(text) > MaxEnglishLen {
		e := fail(CodeInvalidWord, item, text, "「%s」有 %d 個字元，英文詞最多 %d 個；更長的內容請放「我的片語」", text, len(text), MaxEnglishLen)
		e.Limit = MaxEnglishLen
		return Word{}, e
	}
	if !IsEnglish(text) {
		for i := 0; i < len(text); i++ {
			if c := text[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return Word{}, fail(CodeInvalidWord, item, text, "「%s」含有「%c」；英文詞只收英文字母、數字與 - _ .，其他文字請放「我的片語」", text, c)
			}
		}
		return Word{}, fail(CodeInvalidWord, item, text, "「%s」沒有英文字母；英文詞至少要有一個字母", text)
	}
	for _, r := range readings {
		if strings.TrimSpace(r) != "" {
			return Word{}, fail(CodeInvalidReading, item, text, "「%s」是英文詞，不需要讀音", text)
		}
	}
	return Word{Text: text, Readings: []string{}}, nil
}

// Entry 是一筆要加入的詞（讀音可以有多個；英文詞沒有讀音）。
type Entry struct {
	Text     string   `json:"word"`
	Readings []string `json:"readings"`
}

// Add 加入一批詞。已經在詞彙裡的詞合併讀音；整批有任何一筆不合格就全部不加，回傳 *Error。
// 沒有任何實際變更（全部都已存在）時回傳 nil 批次與 nil 錯誤。
func (s *Set) Add(entries []Entry, source string, now int64) (*Batch, error) {
	if err := s.checkBatch(len(entries)); err != nil {
		return nil, err
	}
	b := Batch{Op: "add", Source: source, Time: now}
	pending := map[string]Word{} // 同一批重複出現的詞合併
	var order []string
	for i, e := range entries {
		w, err := Normalize(e.Text, e.Readings, i)
		if err != nil {
			return nil, err
		}
		cur, inBatch := pending[w.Text]
		if !inBatch {
			if old, ok := s.Lookup(w.Text); ok {
				cur = old
			} else {
				cur = Word{Text: w.Text, Source: source, Added: now}
			}
			order = append(order, w.Text)
		}
		for _, r := range w.Readings {
			if !slices.Contains(cur.Readings, r) {
				cur.Readings = append(cur.Readings, r)
			}
		}
		if len(cur.Readings) > MaxReadings {
			e := fail(CodeLimitReadings, i, w.Text, "「%s」加上原有讀音共 %d 個，同一個詞最多 %d 個", w.Text, len(cur.Readings), MaxReadings)
			e.Limit = MaxReadings
			return nil, e
		}
		pending[w.Text] = cur
	}
	added := 0
	for _, text := range order {
		w := pending[text]
		old, ok := s.Lookup(text)
		if ok && slices.Equal(old.Readings, w.Readings) {
			continue
		}
		after := w.clone()
		c := Change{After: &after}
		if ok {
			before := old
			c.Before = &before
		} else {
			added++
		}
		b.Changes = append(b.Changes, c)
	}
	if added > 0 {
		if err := s.checkRoom(added); err != nil {
			return nil, err
		}
	}
	if len(b.Changes) == 0 {
		return nil, nil
	}
	return s.commit(b), nil
}

// Remove 刪除一批詞。不存在的詞整批拒絕（API 與管理頁應該只刪看得到的詞）。
func (s *Set) Remove(texts []string, source string, now int64) (*Batch, error) {
	if err := s.checkBatch(len(texts)); err != nil {
		return nil, err
	}
	b := Batch{Op: "remove", Source: source, Time: now}
	seen := map[string]bool{}
	for i, text := range texts {
		text = strings.TrimSpace(text)
		old, ok := s.Lookup(text)
		if !ok {
			return nil, fail(CodeNotFound, i, text, "「%s」不在自訂詞彙裡", text)
		}
		if seen[text] {
			continue
		}
		seen[text] = true
		b.Changes = append(b.Changes, Change{Before: &old})
	}
	return s.commit(b), nil
}

// SetReadings 改寫一個詞的全部讀音（管理頁點選破音字、刪掉錯的讀音）。
func (s *Set) SetReadings(text string, readings []string, source string, now int64) (*Batch, error) {
	old, ok := s.Lookup(strings.TrimSpace(text))
	if !ok {
		return nil, fail(CodeNotFound, 0, text, "「%s」不在自訂詞彙裡", text)
	}
	w, err := Normalize(old.Text, readings, 0)
	if err != nil {
		return nil, err
	}
	if slices.Equal(w.Readings, old.Readings) {
		return nil, nil
	}
	after := old.clone()
	after.Readings = w.Readings
	before := old
	return s.commit(Batch{Op: "readings", Source: source, Time: now, Changes: []Change{{Before: &before, After: &after}}}), nil
}

// AddPhrases 在「我的片語」最後面加入幾則。
func (s *Set) AddPhrases(texts []string, source string, now int64) (*Batch, error) {
	if err := s.checkBatch(len(texts)); err != nil {
		return nil, err
	}
	if len(s.f.Phrases)+len(texts) > MaxPhrases {
		e := fail(CodeLimitPhrases, -1, "", "「我的片語」最多 %d 則，目前 %d 則，這次要加 %d 則", MaxPhrases, len(s.f.Phrases), len(texts))
		e.Limit, e.Left = MaxPhrases, max(0, MaxPhrases-len(s.f.Phrases))
		return nil, e
	}
	next := uint64(1)
	for _, p := range s.f.Phrases {
		next = max(next, p.ID+1)
	}
	for _, b := range s.f.Batches { // 刪掉的片語的編號也不重用，復原時才不會撞號
		for _, c := range b.Changes {
			if c.PhraseBefore != nil {
				next = max(next, c.PhraseBefore.ID+1)
			}
		}
	}
	b := Batch{Op: "phrase-add", Source: source, Time: now}
	for i, text := range texts {
		n := utf8.RuneCountInString(text)
		if strings.TrimSpace(text) == "" {
			return nil, fail(CodeEmpty, i, text, "第 %d 則片語是空的", i+1)
		}
		if n > MaxPhraseRunes || !utf8.ValidString(text) {
			e := fail(CodeInvalidWord, i, "", "第 %d 則片語有 %d 個字，最多 %d 字", i+1, n, MaxPhraseRunes)
			e.Limit = MaxPhraseRunes
			return nil, e
		}
		p := Phrase{ID: next, Text: text, Added: now}
		next++
		b.Changes = append(b.Changes, Change{PhraseAfter: &p})
	}
	return s.commit(b), nil
}

// RemovePhrases 刪除幾則片語。
func (s *Set) RemovePhrases(ids []uint64, source string, now int64) (*Batch, error) {
	if err := s.checkBatch(len(ids)); err != nil {
		return nil, err
	}
	b := Batch{Op: "phrase-remove", Source: source, Time: now}
	for i, id := range ids {
		k := slices.IndexFunc(s.f.Phrases, func(p Phrase) bool { return p.ID == id })
		if k < 0 {
			return nil, fail(CodeNotFound, i, "", "沒有編號 %d 的片語", id)
		}
		p := s.f.Phrases[k]
		b.Changes = append(b.Changes, Change{PhraseBefore: &p})
	}
	return s.commit(b), nil
}

// MovePhrase 把一則片語移到第 to 個位置（從 0 起）。
func (s *Set) MovePhrase(id uint64, to int, source string, now int64) (*Batch, error) {
	k := slices.IndexFunc(s.f.Phrases, func(p Phrase) bool { return p.ID == id })
	if k < 0 {
		return nil, fail(CodeNotFound, 0, "", "沒有編號 %d 的片語", id)
	}
	to = min(max(to, 0), len(s.f.Phrases)-1)
	if to == k {
		return nil, nil
	}
	p := s.f.Phrases[k]
	s.f.Phrases = slices.Delete(s.f.Phrases, k, k+1)
	s.f.Phrases = slices.Insert(s.f.Phrases, to, p)
	// 移動不記變更明細：復原只把順序換回來，用 UndoOf 以外的方式處理太複雜，所以移動不可整批復原。
	return s.commit(Batch{Op: "phrase-move", Source: source, Time: now}), nil
}

// Undo 復原一個批次：以一個新的批次把它的每筆變更反過來做。
// 那些詞後來又被改過（目前內容和批次的結果不同）時整批拒絕，回傳 CodeConflict。
func (s *Set) Undo(id uint64, source string, now int64) (*Batch, error) {
	k := slices.IndexFunc(s.f.Batches, func(b Batch) bool { return b.ID == id })
	if k < 0 {
		return nil, fail(CodeNotFound, -1, "", "找不到批次 %d（只保留最近 %d 批）", id, KeepBatches)
	}
	orig := s.f.Batches[k]
	if orig.Undone {
		return nil, fail(CodeConflict, -1, "", "批次 %d 已經復原過", id)
	}
	if orig.Op == "phrase-move" {
		return nil, fail(CodeConflict, -1, "", "片語排序不能整批復原")
	}
	b := Batch{Op: "undo", UndoOf: id, Source: source, Time: now}
	added := 0
	for i := len(orig.Changes) - 1; i >= 0; i-- {
		c := orig.Changes[i]
		switch {
		case c.Before != nil || c.After != nil:
			text := pick(c.Before, c.After).Text
			cur, ok := s.Lookup(text)
			if c.After == nil && ok || c.After != nil && (!ok || !slices.Equal(cur.Readings, c.After.Readings)) {
				return nil, fail(CodeConflict, i, text, "「%s」在這批之後又改過，不能整批復原", text)
			}
			if c.Before == nil {
				b.Changes = append(b.Changes, Change{Before: &cur})
				continue
			}
			before := *c.Before
			nc := Change{After: &before}
			if ok {
				nc.Before = &cur
			} else {
				added++
			}
			b.Changes = append(b.Changes, nc)
		default:
			pid := pickPhrase(c.PhraseBefore, c.PhraseAfter).ID
			at := slices.IndexFunc(s.f.Phrases, func(p Phrase) bool { return p.ID == pid })
			if c.PhraseAfter == nil && at >= 0 || c.PhraseAfter != nil && at < 0 {
				return nil, fail(CodeConflict, i, "", "片語 %d 在這批之後又改過，不能整批復原", pid)
			}
			if c.PhraseAfter != nil {
				p := s.f.Phrases[at]
				b.Changes = append(b.Changes, Change{PhraseBefore: &p})
			} else {
				p := *c.PhraseBefore
				b.Changes = append(b.Changes, Change{PhraseAfter: &p})
			}
		}
	}
	if added > 0 {
		if err := s.checkRoom(added); err != nil {
			return nil, err
		}
	}
	s.f.Batches[k].Undone = true
	return s.commit(b), nil
}

func pick(a, b *Word) *Word {
	if a != nil {
		return a
	}
	return b
}

func pickPhrase(a, b *Phrase) *Phrase {
	if a != nil {
		return a
	}
	return b
}

func (s *Set) checkBatch(n int) error {
	if n == 0 {
		return fail(CodeEmpty, -1, "", "沒有要修改的項目")
	}
	if n > MaxBatchItems {
		e := fail(CodeLimitBatch, -1, "", "一次最多修改 %d 筆，這次有 %d 筆；請分批進行", MaxBatchItems, n)
		e.Limit = MaxBatchItems
		return e
	}
	return nil
}

func (s *Set) checkRoom(added int) error {
	if len(s.f.Words) > MaxWords {
		e := fail(CodeOverLimit, -1, "", "自訂詞彙檔有 %d 個詞，超過上限 %d，在刪到上限以內之前不能新增", len(s.f.Words), MaxWords)
		e.Limit = MaxWords
		return e
	}
	if len(s.f.Words)+added > MaxWords {
		left := MaxWords - len(s.f.Words)
		e := fail(CodeLimitWords, -1, "", "自訂詞最多 %d 個，目前 %d 個，這次要新增 %d 個，只剩 %d 個空位", MaxWords, len(s.f.Words), added, left)
		e.Limit, e.Left = MaxWords, left
		return e
	}
	return nil
}

// commit 套用批次的變更、編號、記錄並重建索引。呼叫前已確認變更都合法。
func (s *Set) commit(b Batch) *Batch {
	s.f.Version++
	b.ID = 1
	if n := len(s.f.Batches); n > 0 {
		b.ID = s.f.Batches[n-1].ID + 1
	}
	for i := range b.Changes {
		c := &b.Changes[i]
		switch {
		case c.After != nil:
			c.After.Batch = b.ID
			if k := slices.IndexFunc(s.f.Words, func(w Word) bool { return w.Text == c.After.Text }); k >= 0 {
				s.f.Words[k] = c.After.clone()
			} else {
				s.f.Words = append(s.f.Words, c.After.clone())
			}
		case c.Before != nil:
			s.f.Words = slices.DeleteFunc(s.f.Words, func(w Word) bool { return w.Text == c.Before.Text })
		case c.PhraseAfter != nil:
			c.PhraseAfter.Batch = b.ID
			s.f.Phrases = append(s.f.Phrases, *c.PhraseAfter)
		case c.PhraseBefore != nil:
			id := c.PhraseBefore.ID
			s.f.Phrases = slices.DeleteFunc(s.f.Phrases, func(p Phrase) bool { return p.ID == id })
		}
	}
	s.f.Batches = append(s.f.Batches, b)
	if n := len(s.f.Batches); n > KeepBatches {
		s.f.Batches = slices.Clone(s.f.Batches[n-KeepBatches:])
	}
	s.reindex()
	out := s.f.Batches[len(s.f.Batches)-1]
	return &out
}
