// Package decode 把音節格解碼成文字：詞圖上的 Viterbi，加上組字尾端的前綴感知。
package decode

import (
	"math"
	"sort"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Scorer 提供 P(w | left)。personal.Model 實作它。
type Scorer interface {
	Prob(left, w string) float64
}

// Evidenced 是 Scorer 的選用擴充：把機率拆成系統先驗與個人觀察，供補全計算信心（見 Complete）。
type Evidenced interface {
	SystemProb(left, w string) float64 // 完全不含個人資料的機率
	Evidence(left, w string) float64   // 依操作來源、上下文與時間調整的有效證據
}

// Explicit 是 Scorer 的選用擴充：使用者親手加入的詞（自訂詞，#122）。補全時當作已有 ExplicitEvidence
// 次觀察，剛加入就有中等信心，之後依實際收下的紀錄調整；略過不算拒絕。
// 只在已經打了至少 2 個音節時加，避免打一個常用字就跳出長詞。
type Explicit interface {
	Explicit(w string) bool
	HasExplicit() bool // 有任何親手加入的詞；沒有時呼叫端可以跳過額外的查詢
}

// ExplicitEvidence 是自訂詞在補全時的等效觀察數：補全先驗強度（預設 5）的兩倍，
// 同一前綴只有一個自訂詞時信心約 2/3，兩個以上就分散到門檻附近，打到能分辨為止。
const ExplicitEvidence = 2 * defaultSuggestPrior

// Seg 是解碼結果的一段。
type Seg struct {
	Word       string
	Start, End int
	Partial    bool // 尾端：某個更長的詞的前綴
	Correction bool // 經自動修正讀音到達；介面據此標示
	// Matched 是這個詞實際走的詞庫讀音（空白分隔）：只有讀音與使用者打的音不同（經有成本的近音假設
	// 到達）時才填，其餘為空字串。session 依它辨識近音路徑，不進學習。
	Matched string
}

// Lock 是使用者鎖定的區間：從某位置起 Len 個音節必須是 Word。硬條件。
type Lock struct {
	Segmentation    bool // Explicit word boundary: the locked word cannot merge with neighbors.
	Word            string
	Len             int
	Completion      bool // 鎖定來自 Tab 補全；session 用來區分學習來源
	Suggested       bool // 鎖定來自接受神經次佳提示；只影響代名詞習慣的學習
	LiteralBoundary int  // session 暫時保留被字面文字切開的詞；-1 靠左邊界、+1 靠右邊界，0 為一般鎖定
	// Matched 是跨音手選時詞庫的對應讀音（空白分隔的音節）；精確手選為空字串。
	// 解碼器不看它——鎖定仍只限制「那幾個音節的字」；session 依它把近音讀音留在音節格裡。
	Matched string
}

// matchedReading 回傳詞條讀音相對於使用者打的音（音節格第一個假設）有差異時的讀音鍵，沒有差異為空字串。
func matchedReading(lat input.Lattice, start int, reading []zhuyin.Syllable) string {
	for k, y := range reading {
		if at := start + k; at < len(lat) && len(lat[at]) > 0 && lat[at][0].Syl != y {
			return zhuyin.Key(reading)
		}
	}
	return ""
}

// segMatched 是弧的 Matched：只有走了有成本的假設才可能與打的音不同，零成本弧不必比對。
func segMatched(lat input.Lattice, start int, e lexicon.Entry, cost float64) string {
	if cost <= 0 {
		return ""
	}
	return matchedReading(lat, start, e.Reading)
}

// MaxWordLen 是詞條讀音長度的搜尋上限。
const MaxWordLen = 8

type Decoder struct {
	Left  string // 已確認的文件前詞；空值為 BOS，不寫入語言模型
	Lex   lexicon.Source
	Score Scorer

	// TopM：每個尾端讀音只彙總依系統先驗排序的前 M 個詞（子樹彙總以 unigram 預算，
	// 上下文與個人化只對前 K 名重算）。0 表示預設值。
	TopM int
	// TopB：只對位置上分數最高的 B 個狀態計算尾端前綴。0 表示預設值。
	TopB int
	// SuggestPrior：補全信心的詞群內先驗強度，相當於幾次觀察（見 Complete）。0 表示預設值。
	SuggestPrior float64
	// Beam：每個位置至多展開幾個「有上下文」的狀態（依分數）。0 表示不限。
	// 沒有 bigram 時有上下文的狀態很少，這個上限幾乎不起作用；有 bigram 之後，
	// 大部分前詞都有上下文，需要它把成本壓在延遲預算內（近似，以評測決定大小）。
	Beam int
	// Continue 是尾端「使用者還會繼續打、目標是更長的詞」的權重 γ：讀音比已輸入部分更長的詞，
	// 其機率先乘上 γ 再與剛好打完的詞比較。γ = 1 表示兩者同等看待；0 表示不設（等同 1）。
	Continue             float64
	SeparateContinuation bool // 完整詞與未完成長詞分開競爭，不把同一完整詞重複計入前綴
	// Evidence 回傳同讀音／鄰接條件下的暫時選取權重與總權重。
	// 與一次先驗觀察混合，不寫入 Score。存在時不合併無上下文的狀態。
	Evidence          func(start, end int, left, word string) (accepted, total float64)
	CorrectionPenalty func(start, end int, word string) float64
	CorrectionUsage   func(start int, word string) float64
	// MaxCost 是單一詞（從某個起點走到詞尾的一條讀音）可累計的音節格成本上限；0 表示不限。
	// 近音前端用它界定每詞的換音次數：超過的讀音不展開，不影響成本 0 的原音路徑。
	MaxCost float64
	// MaxCandidates 限制候選查詢實際以上下文評分的詞面數（0 不限）：先依無上下文先驗與讀音權重
	// 挑出前 N 個，其餘不評分也不列出。近音候選用它界定額外候選數與評分成本。
	MaxCandidates int
	// OnlyCosted 讓候選查詢只列舉經由有成本的假設（成本大於 0，例如近音）到達的讀音，
	// 成本 0 的讀音（使用者打的音）略過。與 MaxCandidates 併用時，名額只計有成本的詞面。
	OnlyCosted bool

	cache     map[string][]lexicon.Entry // 尾端讀音 → 依先驗排序的前 M 個系統詞條
	massScale map[string]float64
	longerEnd []int
	// capRepeat 的列舉結果，只在同一次搜尋內有效（見 resetRepeats）
	repeats     map[repeatKey]*repeatWords
	repeatSpans map[[2]int][]spanEntry
	contests    map[int]*spanContest
}

const (
	defaultTopM  = 64
	defaultTopB  = 4
	maxCacheKeys = 4096
	// 補全先驗的等效證據量；不是固定使用次數門檻。
	defaultSuggestPrior = 5
)

// Prior 是可選的系統先驗，只用來排序、不影響分數。personal.Model 實作它。
type Prior interface {
	Prior(w string) float64
}

// Contextual 是可選的介面：回報 P(w | left) 是否可能因 left 而不同。回報 false 的前一個詞，
// 對所有 w 都給出與上下文無關的機率；這類狀態之間只需保留分數最高者（精確，不是近似）。
// 沒有實作此介面的 Scorer 一律視為有上下文。
type Contextual interface {
	HasContext(left string) bool
}

// Layered 讓解碼器把可快取的系統詞庫與會變動的個人部分分開查詢。personal 的詞庫視圖實作它。
type Layered interface {
	System() lexicon.Source
	Adjust([]lexicon.Entry) []lexicon.Entry
	PersonalPrefixed([]zhuyin.Syllable) []lexicon.Entry
}

// Long 是可選的介面：詞條讀音可能超過 MaxWordLen（自訂詞最長 32 音節，#122；系統詞庫的長詞，#142）。
// 超過的部分只靠 HasPrefix 剪枝：一般句子在 MaxWordLen 以後幾乎沒有前綴，不會多找。
type Long = lexicon.Long

// MaxLen 是這個詞庫的讀音長度搜尋上限：MaxWordLen，或詞庫回報的更長的詞。
func MaxLen(lex lexicon.Source) int {
	return max(MaxWordLen, lexicon.Longest(lex))
}

// spans 列舉從 start 起、與音節格相容的所有讀音（以詞庫前綴剪枝）。
func (d *Decoder) spans(lat input.Lattice, start int, fn func(reading []zhuyin.Syllable, cost float64)) {
	limit := MaxLen(d.Lex)
	var walk func(pos int, reading []zhuyin.Syllable, cost float64)
	walk = func(pos int, reading []zhuyin.Syllable, cost float64) {
		if pos == len(lat) || pos-start == limit {
			return
		}
		for _, h := range lat[pos] {
			if d.MaxCost > 0 && cost+h.Cost > d.MaxCost {
				continue
			}
			r := append(reading[:len(reading):len(reading)], h.Syl)
			if !d.Lex.HasPrefix(r) {
				continue
			}
			fn(r, cost+h.Cost)
			walk(pos+1, r, cost+h.Cost)
		}
	}
	walk(start, nil, 0)
}

type state struct {
	last  string
	score float64
	path  []Seg
}

// allowed 判斷詞 w 佔 [i, j) 是否符合鎖定。鎖定限制的是「那幾個音節的字」，不是斷詞：
// 詞完整包住鎖定區間、且該位置的字與鎖定相同時也允許（選「今天」的「天」不會把「今」拆成單字重猜）。
// 被字面文字切開的暫時邊界（LiteralBoundary）仍要求剛好是那個詞。
func allowed(locks map[int]Lock, i, j int, w string) bool {
	var runes []rune
	for s, l := range locks {
		if s < i || s >= j || s == i && l.Word == w && l.Len == j-i {
			continue
		}
		if !l.Segmentation && l.LiteralBoundary == 0 && s+l.Len <= j {
			if runes == nil {
				runes = []rune(w)
			}
			if len(runes) == j-i && utf8.RuneCountInString(l.Word) == l.Len && string(runes[s-i:s-i+l.Len]) == l.Word {
				continue
			}
		}
		return false
	}
	return true
}

type arc struct {
	j    int
	e    lexicon.Entry
	cost float64
}

// Decode 回傳最佳路徑。prefix 為真時，組字尾端可以是「更長的詞的前綴」，
// 其分數為所有相容且顯示字串相同的詞的機率總和，可與完整詞路徑直接比較。
func (d *Decoder) Decode(lat input.Lattice, locks map[int]Lock, prefix bool) []Seg {
	path, _ := d.Best(lat, locks, prefix)
	return path
}

// Best 與 Decode 相同，另外回傳最佳路徑的對數分數；評測用來區分搜尋錯誤與模型錯誤
// （正確答案的分數若比輸出高，就是搜尋漏掉了它）。
func (d *Decoder) Best(lat input.Lattice, locks map[int]Lock, prefix bool) ([]Seg, float64) {
	d.prepareMass(lat)
	n := len(lat)
	if n == 0 {
		return nil, 0
	}
	best := d.forward(lat, locks)

	var winner *state
	consider := func(st state) {
		if winner == nil || st.score > winner.score {
			winner = &st
		}
	}
	for _, st := range best[n] {
		consider(st)
	}
	if prefix {
		topB := d.TopB
		if topB == 0 {
			topB = defaultTopB
		}
		for i := 0; i < n; i++ {
			if lockedIn(locks, i, n) || len(best[i]) == 0 {
				continue
			}
			cands := d.tailEntries(lat, i)
			if len(cands) == 0 {
				continue
			}
			sts := append([]state(nil), best[i]...)
			sort.SliceStable(sts, func(a, b int) bool { return sts[a].score > sts[b].score }) // 同分時保持原順序，結果不隨 Go 版本變
			if len(sts) > topB {
				sts = sts[:topB]
			}
			for _, st := range sts {
				for _, t := range d.group(cands, n-i, st.last, d.Continue) {
					if !t.longer {
						continue
					}
					seg := Seg{Word: t.display, Start: i, End: n, Partial: true, Matched: partialMatched(lat, i, n, t)}
					sc := d.repeatScore(lat, locks, st.path, i, n, t.display, math.Log(d.evidenceMass(i, n, st.last, t.display, t.mass)))
					consider(state{t.display, st.score + sc, append(st.path[:len(st.path):len(st.path)], seg)})
				}
			}
		}
	}
	if winner == nil {
		return nil, math.Inf(-1)
	}
	return winner.path, winner.score
}

// forward 建立所有位置的前向狀態，供整段解碼與候選上下文評分共用。
func (d *Decoder) forward(lat input.Lattice, locks map[int]Lock) [][]state {
	d.resetRepeats()
	n := len(lat)
	// best[i] 是結束於位置 i 的狀態，依前一個詞區分；at[i] 是前一個詞到索引的對照
	best := make([][]state, n+1)
	at := make([]map[string]int, n+1)
	best[0] = []state{{last: d.InitialLeft()}}
	put := func(j int, st state) {
		if at[j] == nil {
			at[j] = map[string]int{}
		}
		if k, ok := at[j][st.last]; ok {
			if st.score > best[j][k].score {
				best[j][k] = st
			}
			return
		}
		at[j][st.last] = len(best[j])
		best[j] = append(best[j], st)
	}
	for i := 0; i < n; i++ {
		if len(best[i]) == 0 {
			continue
		}
		// 從 i 出發的詞與狀態無關：只列舉一次
		arcs := d.wordArcs(lat, i, locks)
		for _, st := range d.frontier(best[i]) {
			for _, a := range arcs {
				sc := st.score + d.repeatScore(lat, locks, st.path, i, a.j, a.e.Surface, d.arcLog(i, a.j, st.last, a.e, a.cost))
				seg := Seg{Word: a.e.Surface, Start: i, End: a.j, Correction: a.e.Correction, Matched: segMatched(lat, i, a.e, a.cost)}
				put(a.j, state{a.e.Surface, sc, append(st.path[:len(st.path):len(st.path)], seg)})
			}
		}
	}

	return best
}

// frontier 回傳需要向前展開的狀態：所有「有上下文」的狀態，加上「無上下文」狀態中分數最高的一個。
// 無上下文的前一個詞對每個候選詞給出相同的機率，所以分數最高者在每一條弧上都勝出。
func (d *Decoder) frontier(states []state) []state {
	c, ok := d.Score.(Contextual)
	if !ok || len(states) <= 1 {
		return states
	}
	out := make([]state, 0, 4)
	plain := -1
	for k, st := range states {
		if d.Evidence != nil || c.HasContext(st.last) {
			out = append(out, st)
		} else if plain < 0 || st.score > states[plain].score {
			plain = k
		}
	}
	if d.Beam > 0 && len(out) > d.Beam {
		sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
		out = out[:d.Beam]
	}
	if plain >= 0 {
		out = append(out, states[plain])
	}
	return out
}

func lockedIn(locks map[int]Lock, i, n int) bool {
	for s := range locks {
		if i <= s && s < n {
			return true
		}
	}
	return false
}

// tail 是尾端某個顯示字串的機率總和與其最佳完整詞。
type tail struct {
	display  string
	mass     float64
	longer   bool // 是否有讀音比已輸入部分更長的詞
	best     lexicon.Entry
	bestP    float64
	bestCost float64
}

type tailEntry struct {
	e    lexicon.Entry
	cost float64
}

// tailEntries 回傳「讀音以 lat[i:] 開頭」的詞：系統詞取依先驗排序的前 M 個（快取），
// 個人片語全部加入（不快取，學習立即生效）。
func (d *Decoder) tailEntries(lat input.Lattice, i int) []tailEntry {
	k := len(lat) - i
	var out []tailEntry
	d.spans(lat, i, func(reading []zhuyin.Syllable, cost float64) {
		if len(reading) != k {
			return
		}
		sys, adjust, personal := d.Lex, func(es []lexicon.Entry) []lexicon.Entry { return es }, []lexicon.Entry(nil)
		if l, ok := d.Lex.(Layered); ok {
			sys, adjust, personal = l.System(), l.Adjust, l.PersonalPrefixed(reading)
		}
		for _, e := range adjust(d.topPrefixed(sys, reading)) {
			out = append(out, tailEntry{e, cost})
		}
		for _, e := range personal {
			out = append(out, tailEntry{e, cost})
		}
	})
	return out
}

func (d *Decoder) topPrefixed(sys lexicon.Source, reading []zhuyin.Syllable) []lexicon.Entry {
	key := zhuyin.Key(reading)
	if es, ok := d.cache[key]; ok {
		return es
	}
	es := append([]lexicon.Entry(nil), sys.Prefixed(reading)...)
	prior := func(w string) float64 { return d.Score.Prob(lm.BOS, w) }
	if p, ok := d.Score.(Prior); ok {
		prior = p.Prior
	}
	ps := make([]float64, len(es))
	for k := range es {
		ps[k] = prior(es[k].Surface) * es[k].PRW
	}
	idx := make([]int, len(es))
	for k := range idx {
		idx[k] = k
	}
	sort.SliceStable(idx, func(a, b int) bool { return ps[idx[a]] > ps[idx[b]] })
	m := d.TopM
	if m == 0 {
		m = defaultTopM
	}
	if len(idx) > m {
		idx = idx[:m]
	}
	top := make([]lexicon.Entry, len(idx))
	for k, x := range idx {
		top[k] = es[x]
	}
	if d.cache == nil || len(d.cache) >= maxCacheKeys {
		d.cache = map[string][]lexicon.Entry{}
	}
	d.cache[key] = top
	return top
}

// partialMatched 是尾端前綴段的 Matched：取最佳詞條讀音中對應已輸入音節的部分。
func partialMatched(lat input.Lattice, start, end int, t *tail) string {
	if t.bestCost <= 0 || len(t.best.Reading) < end-start {
		return ""
	}
	return matchedReading(lat, start, t.best.Reading[:end-start])
}

// group 依顯示字串（詞的前 k 個字）彙總尾端詞條的機率；更長的詞乘上 cont（0 表示不打折）。
func (d *Decoder) group(cands []tailEntry, k int, left string, cont float64) []*tail {
	var order []*tail
	idx := map[string]*tail{}
	// 同一詞面經多條讀音／輸入路徑可達，不表示有更多獨立的使用證據。
	// 和完整路徑的 Viterbi 一樣取最佳路徑，避免前綴彙總重複灌高。
	seen := map[string]float64{}
	for _, c := range cands {
		if d.SeparateContinuation && len(c.e.Reading) <= k {
			continue
		}
		disp := string([]rune(c.e.Surface)[:k])
		weight := c.e.PRW * math.Exp(-c.cost)
		if len(c.e.Reading) > k && cont > 0 {
			weight *= cont
		}
		p := d.Score.Prob(left, c.e.Surface) * weight
		t := idx[disp]
		if t == nil {
			t = &tail{display: disp}
			idx[disp] = t
			order = append(order, t)
		}
		if p > seen[c.e.Surface] {
			t.mass += p - seen[c.e.Surface]
			seen[c.e.Surface] = p
		}
		t.longer = t.longer || len(c.e.Reading) > k
		if p > t.bestP {
			t.best, t.bestP, t.bestCost = c.e, p, c.cost
		}
	}
	return order
}

// Completion 是尾端前綴段的最佳完整詞與其信心，供 Tab 補全使用。
type Completion struct {
	Entry lexicon.Entry
	Conf  float64 // 最佳完整詞的機率 ÷ 同一顯示前綴下所有相容詞的機率總和
}

// CompleteHomophones 比較同一注音前綴的完整延伸詞，不要求目前顯示字形相同。
// 信心以「要補成更長詞」為條件，不是使用者一定會繼續輸入的機率。
// 同一詞面的多讀音只保留最佳路徑，避免重複詞條灌高先驗或個人證據。
func (d *Decoder) CompleteHomophones(lat input.Lattice, start int, left string) (Completion, bool) {
	typed := len(lat) - start
	if typed < 2 || typed >= MaxLen(d.Lex) {
		return Completion{}, false
	}
	xp, _ := d.Score.(Explicit)
	type member struct {
		e               lexicon.Entry
		prior, evidence float64
	}
	var group []member
	seen := map[string]int{}
	ev, _ := d.Score.(Evidenced)
	for _, c := range d.tailEntries(lat, start) {
		if len(c.e.Reading) <= typed || c.e.Correction || c.e.Rare {
			continue
		}
		prior := d.Score.Prob(left, c.e.Surface)
		var evidence float64
		if ev != nil {
			prior, evidence = ev.SystemProb(left, c.e.Surface), ev.Evidence(left, c.e.Surface)
		}
		prior *= c.e.PRW * math.Exp(-c.cost)
		if d.CorrectionUsage != nil {
			evidence += d.CorrectionUsage(start, c.e.Surface)
		}
		if xp != nil && xp.Explicit(c.e.Surface) {
			evidence += ExplicitEvidence
		}
		factor := d.correctionFactor(start, len(lat), c.e.Surface)
		prior *= factor
		evidence *= factor
		m := member{c.e, prior, evidence}
		if i, ok := seen[c.e.Surface]; ok {
			if prior > group[i].prior {
				group[i] = m
			}
		} else {
			seen[c.e.Surface] = len(group)
			group = append(group, m)
		}
	}
	var total, evidence float64
	for _, m := range group {
		total += m.prior
		evidence += m.evidence
	}
	if total <= 0 {
		return Completion{}, false
	}
	kappa := d.SuggestPrior
	if kappa == 0 {
		kappa = defaultSuggestPrior
	}
	var best Completion
	for _, m := range group {
		conf := (m.evidence + kappa*m.prior/total) / (evidence + kappa)
		if conf > best.Conf {
			best = Completion{m.e, conf}
		}
	}
	return best, best.Entry.Surface != ""
}

// Complete 對 path 尾端顯示字串找出最佳完整詞。
// 已成詞也可以延伸，是否允許學習（Partial）不應兼作是否提供補全的開關。
func (d *Decoder) Complete(lat input.Lattice, path []Seg) (Completion, bool) {
	c, _, ok := d.complete(lat, path)
	return c, ok
}

// complete 是 Complete，另回傳尾端詞群的系統先驗總和：剛好打完的詞照算，更長的詞與解碼尾端一樣
// 乘上 Continue（見 CompleteAcross）。
func (d *Decoder) complete(lat input.Lattice, path []Seg) (Completion, float64, bool) {
	if len(path) == 0 {
		return Completion{}, 0, false
	}
	last := path[len(path)-1]
	left := d.InitialLeft()
	if len(path) > 1 {
		left = path[len(path)-2].Word
	}
	k := last.End - last.Start
	ev, _ := d.Score.(Evidenced)
	xp, _ := d.Score.(Explicit)
	type member struct {
		e        lexicon.Entry
		prior, c float64
	}
	var group []member
	var priorSum, cSum float64
	seen := map[string]int{}
	for _, c := range d.tailEntries(lat, last.Start) {
		if string([]rune(c.e.Surface)[:k]) != last.Word {
			continue
		}
		m := member{e: c.e}
		w := c.e.PRW * math.Exp(-c.cost)
		if ev != nil {
			m.prior = ev.SystemProb(left, c.e.Surface) * w
			m.c = ev.Evidence(left, c.e.Surface)
		} else {
			m.prior = d.Score.Prob(left, c.e.Surface) * w
		}
		if d.CorrectionUsage != nil {
			m.c += d.CorrectionUsage(last.Start, c.e.Surface)
		}
		if xp != nil && k >= 2 && xp.Explicit(c.e.Surface) {
			m.c += ExplicitEvidence
		}
		factor := d.correctionFactor(last.Start, last.End, c.e.Surface)
		m.prior *= factor
		m.c *= factor
		if i, ok := seen[c.e.Surface]; ok {
			if m.prior > group[i].prior {
				group[i] = m
			}
		} else {
			seen[c.e.Surface] = len(group)
			group = append(group, m)
		}
	}
	var mass float64
	for _, m := range group {
		priorSum += m.prior
		cSum += m.c
		if len(m.e.Reading) > k && d.Continue > 0 {
			mass += m.prior * d.Continue
		} else {
			mass += m.prior
		}
	}
	if priorSum == 0 {
		return Completion{}, 0, false
	}
	// 在「以這個前綴開頭的詞」之內做貝氏估計：先驗是系統機率在詞群內的佔比，強度相當於 kappa 次觀察；
	// 觀察是依操作來源、上下文與時間調整的有效證據。解碼一次觀察就足以改變首選（選過就記住），
	// 但拿它來決定要不要主動建議太衝動：用過一次「麻婆豆腐」不代表打「麻」就是要它，麻後面還有很多字
	// （使用者回報）。沒有個人觀察時，信心就是系統佔比。
	kappa := d.SuggestPrior
	if kappa == 0 {
		kappa = defaultSuggestPrior
	}
	var best Completion
	for _, m := range group {
		if conf := (m.c + kappa*m.prior/priorSum) / (cSum + kappa); conf > best.Conf {
			best = Completion{Entry: m.e, Conf: conf}
		}
	}
	return best, mass, true
}

// block 是同分候選的排序分區：常用漢字區（U+4E00–9FFF）、其他基本平面、擴充 A 區、其餘平面。
func block(s string) int {
	r := []rune(s)[0]
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:
		return 0
	case r >= 0x3400 && r <= 0x4DBF:
		return 2
	case r <= 0xFFFF:
		return 1
	}
	return 3
}

// Candidate 是候選視窗的一項。
type Candidate struct {
	Entry lexicon.Entry
	Score float64
}

// Candidates 回傳 lat[start:start+length] 的候選，依當前上下文的分數排序；
// pinned 回報為真的詞若排名落後，提前到第 2 位起。
func (d *Decoder) Candidates(lat input.Lattice, start, length int, left string, pinned func(w string) bool) []Candidate {
	d.prepareMass(lat)
	return d.candidates(lat, start, length, func(e lexicon.Entry, cost float64) float64 {
		return d.arcLog(start, start+length, left, e, cost)
	}, pinned, nil)
}

// CandidatesInContext 比較候選鎖定後，左右已輸入音節的最佳完整路徑。
// 選取區間重疊的舊鎖定會被明確替換，其餘鎖定是硬條件；不使用未輸入的文字。
// 前向狀態沿用解碼 beam，右側以共享動態規劃評分，避免每個候選重解整段。
func (d *Decoder) CandidatesInContext(lat input.Lattice, start, length int, locks map[int]Lock, pinned func(string) bool) []Candidate {
	return d.CandidatesInContextWindows(lat, start, length, locks, Window{Lat: lat, Pinned: pinned})[0]
}

// Window 是同一個候選位置的一種讀音格版本：Lat 與基準音節格只有 [start, start+length) 的列不同
// （例如加入近音假設）；MaxCost、MaxCandidates 大於 0 時只對這個版本覆寫 Decoder 的同名上限，
// OnlyCosted 為真時只列出有成本的讀音（見 Decoder.OnlyCosted）。
type Window struct {
	Lat           input.Lattice
	MaxCost       float64
	MaxCandidates int
	OnlyCosted    bool
	Pinned        func(string) bool
	// Accept filters entries before deduplication and the candidate budget.
	// It applies only to this window, including the incomplete-context fallback.
	Accept func(lexicon.Entry) bool
}

// CandidatesInContextWindows 對同一個位置的多個視窗版本各回傳一份候選，共用同一份前後文計算
// （左側前向狀態、右側詞圖、後綴動態規劃與 prepareMass 的前綴保護都取自基準音節格 lat，只算一次；
// 其他版本只在視窗內多了讀音假設，不另算保護）。
// 第一個版本通常就是 lat 本身；其結果與單獨呼叫 CandidatesInContext 完全相同。
func (d *Decoder) CandidatesInContextWindows(lat input.Lattice, start, length int, locks map[int]Lock, windows ...Window) [][]Candidate {
	d.prepareMass(lat)
	end := start + length
	if start < 0 || length <= 0 || end > len(lat) {
		return make([][]Candidate, len(windows))
	}
	kept := map[int]Lock{}
	for at, lock := range locks {
		if at >= end || at+lock.Len <= start {
			kept[at] = lock
		}
	}
	fw := d.forward(lat[:start], kept)
	before := d.frontier(fw[start])
	// 每個位置的詞圖只列舉一次。
	arcs := make([][]arc, len(lat))
	for at := end; at < len(lat); at++ {
		d.spans(lat, at, func(reading []zhuyin.Syllable, cost float64) {
			for _, e := range d.Lex.Exact(reading) {
				j := at + len(reading)
				if allowed(kept, at, j, e.Surface) {
					arcs[at] = append(arcs[at], arc{j, e, cost})
				}
			}
		})
	}
	type key struct {
		at   int
		left string
	}
	cache := map[key]float64{}
	var suffix func(int, string) float64
	suffix = func(at int, left string) float64 {
		if at == len(lat) {
			return 0
		}
		k := key{at, left}
		if v, ok := cache[k]; ok {
			return v
		}
		best := math.Inf(-1)
		for _, a := range arcs[at] {
			score := d.arcLog(at, a.j, left, a.e, a.cost) + suffix(a.j, a.e.Surface)
			best = math.Max(best, score)
		}
		cache[k] = best
		return best
	}
	inner := d.innerScores(lat, fw, start, end, kept, suffix)
	out := make([][]Candidate, len(windows))
	maxCost, maxCandidates, onlyCosted := d.MaxCost, d.MaxCandidates, d.OnlyCosted
	defer func() { d.MaxCost, d.MaxCandidates, d.OnlyCosted = maxCost, maxCandidates, onlyCosted }()
	for i, w := range windows {
		d.MaxCost, d.MaxCandidates, d.OnlyCosted = maxCost, maxCandidates, onlyCosted || w.OnlyCosted
		if w.MaxCost > 0 {
			d.MaxCost = w.MaxCost
		}
		if w.MaxCandidates > 0 {
			d.MaxCandidates = w.MaxCandidates
		}
		// 整段不能成詞時回退左側排序，仍讓使用者能逐字修復。
		viable := false
		out[i] = d.candidates(w.Lat, start, length, func(e lexicon.Entry, cost float64) float64 {
			best := math.Inf(-1)
			for _, st := range before {
				best = math.Max(best, st.score+d.arcLog(start, end, st.last, e, cost))
			}
			score := best + suffix(end, e.Surface)
			if v, ok := inner[innerKey(e.Surface, e.Reading)]; ok && v > score {
				score = v
			}
			viable = viable || !math.IsInf(score, -1)
			return score
		}, w.Pinned, w.Accept)
		if !viable && len(out[i]) > 0 { // 沒有任何候選時回退也是空的，不必再列舉一次
			left := d.InitialLeft()
			best := math.Inf(-1)
			for _, st := range before {
				if st.score > best {
					left, best = st.last, st.score
				}
			}
			d.prepareMass(w.Lat)
			out[i] = d.candidates(w.Lat, start, length, func(e lexicon.Entry, cost float64) float64 {
				return d.arcLog(start, end, left, e, cost)
			}, w.Pinned, w.Accept)
		}
	}
	return out
}

// innerScores 回傳 [start, end) 的候選若是較長詞的一部分時，經過那個詞的最佳完整路徑分數（#172）。
// 選了候選之後，解碼器可以把跨過它的詞組回來（鎖定只限制那幾個字，見 allowed），所以「空氣」裡的
// 「氣」選完仍是「空氣」，候選分數也應該算這條路。只把候選當成獨立一個詞評分時，bigram 是依詞庫
// 斷詞計數的：「空→氣」被「空氣」吸收而沒有配對，沒成詞的罕用異體（空→気）反而有，詞頻低幾百倍
// 的字就排到前面。鍵是候選詞面與讀音（innerKey）；fw 是 forward(lat[:start]) 的結果。
func (d *Decoder) innerScores(lat input.Lattice, fw [][]state, start, end int, kept map[int]Lock, suffix func(int, string) float64) map[string]float64 {
	out := map[string]float64{}
	limit := MaxLen(d.Lex)
	for s := max(0, end-limit); s <= start; s++ {
		if len(fw[s]) == 0 {
			continue
		}
		states := d.frontier(fw[s])
		d.spans(lat, s, func(reading []zhuyin.Syllable, cost float64) {
			j := s + len(reading)
			if j < end || s == start && j == end {
				return
			}
			for _, e := range d.Lex.Exact(reading) {
				runes := []rune(e.Surface)
				if len(runes) != j-s || !allowed(kept, s, j, e.Surface) {
					continue
				}
				best := math.Inf(-1)
				for _, st := range states {
					best = math.Max(best, st.score+d.arcLog(s, j, st.last, e, cost))
				}
				score := best + suffix(j, e.Surface)
				if math.IsInf(score, -1) {
					continue
				}
				k := innerKey(string(runes[start-s:end-s]), reading[start-s:end-s])
				if v, ok := out[k]; !ok || score > v {
					out[k] = score
				}
			}
		})
	}
	return out
}

func innerKey(surface string, reading []zhuyin.Syllable) string {
	return surface + "\x00" + zhuyin.Key(reading)
}

func (d *Decoder) candidates(lat input.Lattice, start, length int, score func(lexicon.Entry, float64) float64, pinned func(string) bool, accept func(lexicon.Entry) bool) []Candidate {
	var out []Candidate
	seen := map[string]int{}
	consider := func(e lexicon.Entry, cost float64) {
		c := Candidate{e, score(e, cost)}
		if k, ok := seen[e.Surface]; ok {
			if c.Score > out[k].Score {
				out[k] = c
			}
		} else {
			seen[e.Surface] = len(out)
			out = append(out, c)
		}
	}
	var pool []tailEntry
	d.spans(lat[:start+length], start, func(reading []zhuyin.Syllable, cost float64) {
		if len(reading) != length || d.OnlyCosted && cost <= 0 {
			return
		}
		for _, e := range d.Lex.Exact(reading) {
			if accept != nil && !accept(e) {
				continue
			}
			if d.MaxCandidates > 0 {
				pool = append(pool, tailEntry{e, cost})
				continue
			}
			consider(e, cost)
		}
	})
	for _, p := range d.topPool(pool, d.MaxCandidates) {
		consider(p.e, p.cost)
	}
	sort.SliceStable(out, func(a, b int) bool {
		ea, eb := out[a].Entry, out[b].Entry
		if ea.Rare != eb.Rare { // 罕用字排在後面：多半是字型沒有的擴充區字（使用者回報「亂碼」）
			return !ea.Rare
		}
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		if ba, bb := block(ea.Surface), block(eb.Surface); ba != bb {
			return ba < bb
		}
		return ea.Surface < eb.Surface
	})
	if pinned != nil && len(out) > 2 {
		rest := append([]Candidate(nil), out[2:]...)
		for _, c := range rest {
			if !pinned(c.Entry.Surface) {
				continue
			}
			for k := range out {
				if out[k].Entry.Surface == c.Entry.Surface {
					copy(out[2:k+1], out[1:k])
					out[1] = c
					break
				}
			}
		}
	}
	return out
}

// topPool 依無上下文先驗、讀音權重與音節格成本挑出前 n 個詞面（同詞面取排名最高的讀音）。
// n 不大於 0 時不挑選。排名只用來決定誰進入上下文評分，不影響入選者的最終分數。
func (d *Decoder) topPool(pool []tailEntry, n int) []tailEntry {
	if n <= 0 || len(pool) == 0 {
		return nil
	}
	prior := func(w string) float64 { return d.Score.Prob(lm.BOS, w) }
	if p, ok := d.Score.(Prior); ok {
		prior = p.Prior
	}
	rank := make([]float64, len(pool))
	for i, p := range pool {
		rank[i] = prior(p.e.Surface) * p.e.PRW * math.Exp(-p.cost)
	}
	idx := make([]int, len(pool))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rank[idx[a]] > rank[idx[b]] })
	out := make([]tailEntry, 0, min(n, len(pool)))
	seen := map[string]bool{}
	for _, i := range idx {
		if seen[pool[i].e.Surface] {
			continue
		}
		seen[pool[i].e.Surface] = true
		out = append(out, pool[i])
		if len(out) == n {
			break
		}
	}
	return out
}

// InitialLeft 統一解碼、候選、補全及診斷的組字起點。
func (d *Decoder) InitialLeft() string {
	if d.Left != "" {
		return d.Left
	}
	return lm.BOS
}
