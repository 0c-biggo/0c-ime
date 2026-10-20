package engine

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 自訂詞彙的管理指令（#122）。設定程式、本機 API 與 0c-update vocab 共用同一組指令，
// 由唯一的寫入者（引擎）在自己的鎖內執行；結果不含按鍵或句子，只有使用者加入或學到的詞。

// 指令。
const (
	VocabList         = "list"          // 自訂詞，可用 Query 篩選詞面或讀音、Offset／Limit 分頁
	VocabAdd          = "add"           // Words
	VocabRemove       = "remove"        // Texts
	VocabReadings     = "readings"      // Text、Readings：改寫一個詞的全部讀音
	VocabUndo         = "undo"          // Batch
	VocabBatches      = "batches"       // 最近的變更（新的在前）；Batch 不為 0 時只回傳那一批的明細
	VocabRank         = "rank"          // Texts（空白表示全部，最多 Limit 筆）：只打這組注音時排第幾、前面是哪個詞；只讀不學
	VocabLearned      = "learned"       // 自動學到的片語
	VocabKeep         = "keep"          // Texts：學到的片語轉成自訂詞
	VocabForget       = "forget"        // Texts：撤銷自動學到的詞（與輸入法的 Forget 相同）
	VocabPhrases      = "phrases"       // 「我的片語」
	VocabPhraseAdd    = "phrase-add"    // Texts
	VocabPhraseRemove = "phrase-remove" // IDs
	VocabPhraseMove   = "phrase-move"   // ID、To
	VocabStatus       = "status"        // 版本與數量
)

// VocabRequest 是一個管理指令。
type VocabRequest struct {
	Op       string        `json:"op"`
	Source   string        `json:"source,omitempty"` // vocab.Source*；空白為 manual
	Words    []vocab.Entry `json:"words,omitempty"`
	Texts    []string      `json:"texts,omitempty"`
	Text     string        `json:"text,omitempty"`
	Readings []string      `json:"readings,omitempty"`
	// Expect 是 readings 指令時使用者看到的原讀音：目前的讀音已經不同（AI 助手或另一個視窗剛改過）就拒絕，
	// 回傳 conflict，不悄悄覆蓋別人的修改。nil 表示不檢查。
	Expect []string `json:"expect,omitempty"`
	Batch  uint64   `json:"batch,omitempty"`
	IDs    []uint64 `json:"ids,omitempty"`
	ID     uint64   `json:"id,omitempty"`
	To     int      `json:"to,omitempty"`
	Query  string   `json:"query,omitempty"`
	Offset int      `json:"offset,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

// VocabResponse 是指令的結果。Error 不為 nil 時其他欄位沒有意義（Version、Count 仍正確）。
type VocabResponse struct {
	OK          bool           `json:"ok"`
	Error       *vocab.Error   `json:"error,omitempty"`
	Version     uint64         `json:"version"`
	Count       int            `json:"count"` // 有效的自訂詞數
	Limit       int            `json:"limit"` // vocab.MaxWords
	Stored      int            `json:"stored,omitempty"`
	Notes       []string       `json:"notes,omitempty"` // 載入時沒有使用的內容（手動改壞、超過上限）
	Total       int            `json:"total,omitempty"` // 篩選後的總數（分頁用）
	Words       []vocab.Word   `json:"words,omitempty"`
	Batch       *vocab.Batch   `json:"batch,omitempty"`
	Batches     []BatchSummary `json:"batches,omitempty"`
	Ranks       []Rank         `json:"ranks,omitempty"`
	Learned     []Learned      `json:"learned,omitempty"`
	Phrases     []vocab.Phrase `json:"phrases,omitempty"`
	Annotations []Annotation   `json:"annotations,omitempty"`
	Changed     bool           `json:"changed,omitempty"` // 這個指令改了資料，宿主要存檔
}

// BatchSummary 是批次清單的一項；明細用 VocabBatches 加 Batch 查詢。
type BatchSummary struct {
	ID      uint64   `json:"id"`
	Time    int64    `json:"time"`
	Source  string   `json:"source"`
	Op      string   `json:"op"`
	UndoOf  uint64   `json:"undo_of,omitempty"`
	Undone  bool     `json:"undone,omitempty"`
	Count   int      `json:"count"`
	Preview []string `json:"preview"` // 前幾個詞（片語取前 20 字）
}

// Rank 是一個詞在只打它的注音時的位置。
type Rank struct {
	Text    string `json:"word"`
	Reading string `json:"reading"`
	First   bool   `json:"first"`            // 整段解碼直接是這個詞
	Page    int    `json:"page"`             // 在候選窗第幾頁（從 1 起）；0 表示不在候選裡
	Index   int    `json:"index"`            // 在候選裡第幾個（從 1 起）
	Ahead   string `json:"ahead,omitempty"`  // 排在它前面的第一個詞（First 時為空）
	Output  string `json:"output,omitempty"` // 整段解碼的結果
	Learned bool   `json:"learned"`          // 使用者用這個讀音選過它
}

// Learned 是自動學到的詞：晉升的個人片語，或使用者親手選過讀音的多字詞。
type Learned struct {
	Text    string `json:"word"`
	Reading string `json:"reading"`
	Kind    string `json:"kind"`  // phrase：相鄰手選累積晉升的片語；reading：親手選過這個讀音的詞
	Uses    int    `json:"uses"`  // 片語累積出現的次數；reading 為 0
	Vocab   bool   `json:"vocab"` // 已經是自訂詞
}

// candidatePage 是計算 Rank.Page 用的每頁候選數（直排的預設）。
const candidatePage = 9

// Vocab 執行一個管理指令。引擎鎖也是按鍵用的鎖，可能耗時的唯讀指令不能整段拿著它：
// annotate 只查內建詞庫（不可變），完全不拿鎖；rank 每個詞各拿一次。
func (e *Engine) Vocab(req VocabRequest) VocabResponse {
	switch req.Op {
	case VocabAnnotate:
		resp := VocabResponse{OK: true, Annotations: e.annotate(req.Texts)}
		e.fillStatus(&resp)
		return resp
	case VocabRank:
		resp := VocabResponse{OK: true, Ranks: e.ranks(req.Texts, req.Limit)}
		e.fillStatus(&resp)
		return resp
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vocabLocked(req)
}

func (e *Engine) fillStatus(resp *VocabResponse) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v := e.Model.Vocab; v != nil {
		resp.Version, resp.Count, resp.Stored, resp.Notes = v.Version(), v.Len(), v.Stored(), v.Problems()
	}
	resp.Limit = vocab.MaxWords
}

func (e *Engine) vocabLocked(req VocabRequest) VocabResponse {
	m := e.Model
	if m.Vocab == nil {
		m.Vocab = vocab.New()
	}
	src := req.Source
	if src == "" {
		src = vocab.SourceManual
	}
	var (
		resp VocabResponse
		b    *vocab.Batch
		err  error
	)
	switch req.Op {
	case VocabStatus:
	case VocabList:
		resp.Words, resp.Total = listWords(m.Vocab.Words(), req.Query, req.Offset, req.Limit)
	case VocabAdd:
		b, err = m.VocabAdd(req.Words, src)
	case VocabRemove:
		b, err = m.VocabRemove(req.Texts, src)
	case VocabReadings:
		if cur, ok := m.Vocab.Lookup(strings.TrimSpace(req.Text)); ok && req.Expect != nil && !slices.Equal(cur.Readings, req.Expect) {
			err = &vocab.Error{Code: vocab.CodeConflict, Item: 0, Text: cur.Text, Msg: "「" + cur.Text + "」剛被修改過（可能是 AI 助手或另一個視窗），已換成最新的內容，請再改一次"}
			break
		}
		b, err = m.VocabSetReadings(req.Text, req.Readings, src)
	case VocabUndo:
		b, err = m.VocabUndo(req.Batch, src)
	case VocabBatches:
		if req.Batch != 0 {
			got, ok := m.Vocab.Batch(req.Batch)
			if !ok {
				err = &vocab.Error{Code: vocab.CodeNotFound, Item: -1, Msg: "找不到這個批次"}
				break
			}
			resp.Batch = &got
			break
		}
		for _, x := range m.Vocab.Batches() {
			resp.Batches = append(resp.Batches, summarize(x))
		}
	case VocabLearned:
		resp.Learned = e.learned()
	case VocabKeep:
		var entries []vocab.Entry
		learned := map[string][]string{}
		for _, l := range e.learned() {
			learned[l.Text] = append(learned[l.Text], l.Reading)
		}
		for i, t := range req.Texts {
			rks, ok := learned[t]
			if !ok {
				err = &vocab.Error{Code: vocab.CodeNotFound, Item: i, Text: t, Msg: "「" + t + "」不是學到的詞"}
				break
			}
			entries = append(entries, vocab.Entry{Text: t, Readings: rks[:min(len(rks), vocab.MaxReadings)]})
		}
		if err == nil {
			b, err = m.VocabAdd(entries, vocab.SourceLearned)
		}
	case VocabForget:
		if len(req.Texts) == 0 {
			err = &vocab.Error{Code: vocab.CodeEmpty, Item: -1, Msg: "沒有要忘記的詞"}
			break
		}
		for _, t := range req.Texts {
			if t = strings.TrimSpace(t); t != "" {
				m.Forget(t)
			}
		}
		resp.Changed = true
	case VocabPhrases:
	case VocabPhraseAdd:
		b, err = m.Vocab.AddPhrases(req.Texts, src, e.now())
		e.phrasesChanged(b)
	case VocabPhraseRemove:
		b, err = m.Vocab.RemovePhrases(req.IDs, src, e.now())
		e.phrasesChanged(b)
	case VocabPhraseMove:
		b, err = m.Vocab.MovePhrase(req.ID, req.To, src, e.now())
		e.phrasesChanged(b)
	default:
		err = &vocab.Error{Code: "unknown_op", Item: -1, Msg: "未知的詞彙指令：" + req.Op}
	}
	if err != nil {
		if ve, ok := err.(*vocab.Error); ok {
			resp.Error = ve
		} else {
			resp.Error = &vocab.Error{Code: "error", Item: -1, Msg: err.Error()}
		}
	} else {
		resp.OK = true
	}
	if b != nil {
		resp.Batch, resp.Changed = b, true
	}
	if req.Op == VocabPhrases || strings.HasPrefix(req.Op, "phrase-") {
		resp.Phrases = m.Vocab.Phrases()
	}
	resp.Version, resp.Count, resp.Limit, resp.Stored, resp.Notes = m.Vocab.Version(), m.Vocab.Len(), vocab.MaxWords, m.Vocab.Stored(), m.Vocab.Problems()
	return resp
}

// VocabSnapshot 在引擎鎖內複製自訂詞彙，讓宿主在鎖外存檔；沒有自訂詞彙時回傳 nil。
func (e *Engine) VocabSnapshot() *vocab.Set {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Model.Vocab == nil {
		return nil
	}
	return e.Model.Vocab.Clone()
}

// SetVocab 換上宿主載入的自訂詞彙。
func (e *Engine) SetVocab(v *vocab.Set) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Model.Vocab = v
	e.Model.Gen++
}

func (e *Engine) now() int64 {
	if e.Model.Now == nil {
		return 0
	}
	return e.Model.Now()
}

// phrasesChanged：片語不影響選字，但 = 選單的「我的片語」分頁要重新整理。
func (e *Engine) phrasesChanged(b *vocab.Batch) {
	if b != nil {
		e.Model.Gen++
	}
}

func listWords(all []vocab.Word, query string, offset, limit int) ([]vocab.Word, int) {
	query = strings.TrimSpace(query)
	if query != "" {
		key := zhuyin.Key(zhuyin.ParseReading(query))
		all = slices.DeleteFunc(all, func(w vocab.Word) bool {
			if strings.Contains(w.Text, query) {
				return false
			}
			for _, r := range w.Readings {
				if r == key || strings.HasPrefix(r, key+" ") {
					return false
				}
			}
			return true
		})
	}
	total := len(all)
	offset = min(max(offset, 0), total)
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	return all[offset:min(total, offset+limit)], total
}

func summarize(b vocab.Batch) BatchSummary {
	s := BatchSummary{ID: b.ID, Time: b.Time, Source: b.Source, Op: b.Op, UndoOf: b.UndoOf, Undone: b.Undone, Count: len(b.Changes), Preview: []string{}}
	for _, c := range b.Changes {
		if len(s.Preview) == 5 {
			break
		}
		switch {
		case c.After != nil:
			s.Preview = append(s.Preview, c.After.Text)
		case c.Before != nil:
			s.Preview = append(s.Preview, c.Before.Text)
		case c.PhraseAfter != nil:
			s.Preview = append(s.Preview, clip(c.PhraseAfter.Text, 20))
		case c.PhraseBefore != nil:
			s.Preview = append(s.Preview, clip(c.PhraseBefore.Text, 20))
		}
	}
	return s
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// ranks 對每個詞的每個讀音，看只打這組注音（句首、沒有前文）時的結果。只讀不學。
// 每個讀音各拿一次引擎鎖，幾百個詞的排名不會卡住打字。
func (e *Engine) ranks(texts []string, limit int) []Rank {
	e.mu.Lock()
	m := e.Model
	var words []vocab.Word
	if m.Vocab != nil {
		words = m.Vocab.Words()
		if len(texts) > 0 {
			words = words[:0:0]
			for _, t := range texts {
				if w, ok := m.Vocab.Lookup(strings.TrimSpace(t)); ok {
					words = append(words, w)
				}
			}
		}
	}
	e.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var out []Rank
	for _, w := range words {
		for _, rk := range w.Readings {
			if len(out) >= limit {
				return out
			}
			out = append(out, e.rankOne(w.Text, rk))
		}
	}
	return out
}

func (e *Engine) rankOne(word, rk string) Rank {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.Model
	dec := &decode.Decoder{Lex: m.Source(), Score: m}
	syls := zhuyin.ParseReading(rk)
	lat := input.Exact{}.Expand(syls)
	r := Rank{Text: word, Reading: rk, Learned: m.Store.Readings[word+"\t"+rk]}
	for _, g := range dec.Decode(lat, nil, false) {
		r.Output += g.Word
	}
	r.First = r.Output == word
	pinned := func(x string) bool { return m.VocabReading(x, syls) }
	for i, c := range dec.Candidates(lat, 0, len(syls), lm.BOS, pinned) {
		if c.Entry.Surface == word {
			r.Index, r.Page = i+1, i/candidatePage+1
			break
		}
		if r.Ahead == "" {
			r.Ahead = c.Entry.Surface
		}
	}
	switch {
	case r.First:
		r.Ahead = ""
	case r.Ahead == "" && r.Index > 0:
		// 同長度的候選裡它排第一，但組字解成別的拆法（例如學過的「士｜傑」）：贏過它的是解碼結果。
		// 否則 0c-update 印出「第 1 頁第 1 個，前面是「」」，使用者看不出為什麼不是首選。
		r.Ahead = r.Output
	}
	return r
}

func (e *Engine) learned() []Learned {
	m := e.Model
	out := make([]Learned, 0, len(m.Store.PhraseOrder))
	seen := map[string]bool{}
	for _, ph := range m.Store.PhraseOrder {
		rk, ok := m.Store.Phrases[ph]
		if !ok {
			continue
		}
		seen[ph+"\t"+rk] = true
		out = append(out, Learned{Text: ph, Reading: rk, Kind: "phrase", Uses: m.Store.PhraseSeen[ph], Vocab: m.IsVocab(ph)})
	}
	// 親手選過讀音的多字詞（單字的手選是同音字偏好，不列）；依詞面排序，結果可重現。
	var picks []Learned
	for k := range m.Store.Readings {
		w, rk, _ := strings.Cut(k, "\t")
		if seen[k] || utf8.RuneCountInString(w) < 2 || m.Store.Tombstones[w] {
			continue
		}
		picks = append(picks, Learned{Text: w, Reading: rk, Kind: "reading", Vocab: m.IsVocab(w)})
	}
	slices.SortFunc(picks, func(a, b Learned) int { return strings.Compare(a.Text+"\t"+a.Reading, b.Text+"\t"+b.Reading) })
	return append(out, picks...)
}
