// Package decode 把音節格解碼成文字：詞圖上的 Viterbi，加上組字尾端的前綴感知。
package decode

import (
	"math"
	"slices"
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
	// Cuts 是每個音節結束的位置（注音九宮格自動切字：位置是鍵，一個音節佔 1–3 顆鍵），用 Ends 讀。每個音節
	// 都只佔一個位置時是 nil；一般的音節格永遠是 nil，序列化的結果跟沒有這個欄位時相同。存指標是為了不讓 Seg 變大：
	// 解碼時路徑一直在複製，一般的音節格每一段只多一個 nil 指標。
	Cuts *[]int `json:",omitempty"`
}

// Ends 是 Cuts 的內容；每個音節佔一個位置時是 nil。
func (g Seg) Ends() []int { return deref(g.Cuts) }

// Syllables 是這一段的音節數：Cuts 的長度，或每個音節佔一個位置時的 End-Start。
func (g Seg) Syllables() int { return syllables(g.Start, g.End, deref(g.Cuts)) }

func deref(c *[]int) []int {
	if c == nil {
		return nil
	}
	return *c
}

// ref 是 Cuts 存在 Seg、弧與尾端詞條裡的樣子：nil 時不配置。
func ref(c []int) *[]int {
	if c == nil {
		return nil
	}
	return &c
}

func syllables(start, end int, cuts []int) int {
	if cuts != nil {
		return len(cuts)
	}
	return end - start
}

// charAt 回報位置 pos 在從 start 起、音節結束於 cuts（nil 時每個音節佔一個位置）的讀音裡是第幾個音節的開頭；
// pos 不是音節邊界時回傳 false。pos 是讀音的終點時回傳音節數。
func charAt(start int, cuts []int, pos int) (int, bool) {
	if cuts == nil {
		return pos - start, true
	}
	if pos == start {
		return 0, true
	}
	for k, c := range cuts {
		if c == pos {
			return k + 1, true
		}
	}
	return 0, false
}

// sylStart 是從 start 起、音節結束於 cuts 的讀音裡第 k 個音節開頭的位置。
func sylStart(start int, cuts []int, k int) int {
	if cuts == nil || k == 0 {
		return start + k
	}
	return cuts[k-1]
}

// JoinCuts 接起相鄰幾段的 Cuts；每一段都是 nil（每個音節佔一個位置）時回傳 nil。
func JoinCuts(segs []Seg) []int {
	graph := false
	for _, g := range segs {
		graph = graph || g.Cuts != nil
	}
	if !graph {
		return nil
	}
	var out []int
	for _, g := range segs {
		if g.Cuts != nil {
			out = append(out, *g.Cuts...)
			continue
		}
		for at := g.Start + 1; at <= g.End; at++ {
			out = append(out, at)
		}
	}
	return out
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
// cuts 是讀音每個音節結束的位置（nil 時每個音節佔一個位置，見 Seg.Cuts）。
func matchedReading(lat input.Lattice, start int, reading []zhuyin.Syllable, cuts []int) string {
	for k, y := range reading {
		if at := sylStart(start, cuts, k); at < len(lat) && len(lat[at]) > 0 && (lat[at][0].Syl != y && !untonedHyp(lat[at], y) || correctedOnly(lat[at], y)) {
			return zhuyin.Key(reading)
		}
	}
	return ""
}

// correctedOnly 回報 y 在這個位置只是觸控修正的讀音（input.Hyp.Corrected）。一般的位置第一個假設是打的音，修正讀音都
// 跟它不同，上面的比較已經算到；觸控鍵盤的合併弧（session 的 merge.go）合成的位置整列都是修正，第一個也是。
func correctedOnly(row []input.Hyp, y zhuyin.Syllable) bool {
	found := false
	for _, h := range row {
		if h.Syl == y {
			if !h.Corrected {
				return false
			}
			found = true
		}
	}
	return found
}

// untonedHyp 回報 y 是否為這個位置沒打聲調或簡拼展開出來的讀音：那是使用者打的音，不是近音。
func untonedHyp(row []input.Hyp, y zhuyin.Syllable) bool {
	for _, h := range row {
		if h.Untoned && h.Syl == y {
			return true
		}
	}
	return false
}

// segMatched 是弧的 Matched：只有走了有成本的假設才可能與打的音不同，零成本弧不必比對。
func segMatched(lat input.Lattice, start int, e lexicon.Entry, cost float64, cuts []int) string {
	if cost <= 0 {
		return ""
	}
	return matchedReading(lat, start, e.Reading, cuts)
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
	// PrefixCacheKeys 是尾端前綴快取（topPrefixed）最多保留幾個讀音，每個讀音存前 TopM 個詞條。
	// 0 表示預設 4096，負數不快取。快取只影響速度、不影響答案（#307），手機用較小的上限壓住記憶體。
	PrefixCacheKeys int

	// cache 是尾端讀音 → 依先驗排序的前 M 個系統詞條，分兩代：新的一代滿了（上限的一半）就變成舊的一代、舊的丟掉；
	// 查到舊一代的讀音搬回新的一代。常用的短讀音（一個音節的開頭有上千個詞條，最貴）因此一直留著，不會每到上限就
	// 整份清掉重算（自動切字每一鍵要查上百個讀音，512 的上限下一半以上落空）。
	cache, cacheOld map[string][]lexicon.Entry
	cacheKey        prefixKey // cache 的失效條件（prefixVersion）
	massScale       map[massKey]float64
	homophones      map[string]homophones // languageProbability 的同音詞，跟 massScale 一起失效
	homLex          lexicon.Source
	// bosProb 的快取：bosMemo 依 BestSearch 的 searchKey（評分源與它的版本）保存，bos 只在 BestSearch 裡指向它。
	bos, bosMemo map[string]float64
	bosKey       searchKey
	longerEnd    []int
	// prepareMass 的 longerFrom 記在 longerMemo（依起點那一列的編號），rowIDs 是列的內容 → 編號。
	longerMemo    map[uint32][]longerEntry
	rowIDs        map[string]uint32
	longerMemoKey longerMemoKey
	// FreshMass 讓每次搜尋都重建 massScale，不跨搜尋保留；給快取開／關比對的測試用。
	FreshMass bool
	// width 是目前搜尋的音節格最寬的假設佔幾個位置（input.Lattice.Width）；大於 1 是按鍵圖（自動切字），
	// 由 prepareMass 設定。
	width int
	// massScale 的失效條件：評分源與個人資料版本（MassVersion）。
	massScorer any
	massGen    int
	massTime   int64
	// capRepeat 的列舉結果，只在同一次搜尋內有效（見 resetRepeats）
	repeats     map[repeatKey]*repeatWords
	repeatSpans map[[3]int][]spanEntry
	contests    map[[3]int]*spanContest
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

// continuationRow：音節格某位置的假設多於這個數時，改用 lexicon.Continuations 剪枝。這只是換一種查法：
// Continuer 的結果與逐一問 HasPrefix 相同，答案不變，所以照列長決定（簡拼、沒打聲調加模糊音或鄰鍵、九宮格的
// 寬列都適用）。弧數上限與裁切方式看的是簡拼的明確標記（abbreviatedSpan），不是這個數。
const continuationRow = 16

// span 是 spans 列舉的一條讀音：結束在 end，cuts 是每個音節結束的位置（每個音節都只佔一個位置時是 nil，
// 一般的音節格永遠是 nil，見 Seg.Cuts），cost 是走過的假設成本總和。
type span struct {
	reading []zhuyin.Syllable
	end     int
	cuts    []int
	cost    float64
	// untoned／abbrev：路徑走過 input.Hyp.Untoned／Abbreviated 的假設。按鍵圖用它們決定弧數上限：
	// 同一個位置有好幾種寬度的假設，看位置會把沒走簡拼展開的弧也算進去。一般的音節格照舊看位置（untonedSpan）。
	untoned, abbrev bool
	// corrected：路徑走過觸控修正的假設（input.Hyp.Corrected）。
	corrected bool
}

// spans 列舉從 start 起、與音節格相容的所有讀音（以詞庫前綴剪枝）。佔多個位置的假設（input.Hyp.Span，按鍵圖）
// 走到 pos+Span，超出音節格的不走；一般的音節格每個假設佔一個位置，end 一律是 start+len(reading)。
func (d *Decoder) spans(lat input.Lattice, start int, fn func(sp span)) {
	d.spansSeen(lat, start, fn, nil)
}

// spansSeen 同 spans；seen 不是 nil 時每讀一列就以那一列的位置呼叫一次，結果跟音節格的長度有關（走到結尾、
// 假設超出結尾）時以 -1 呼叫。給 longerFrom 的記憶用：結果只取決於讀過的列與那時的長度。
func (d *Decoder) spansSeen(lat input.Lattice, start int, fn func(sp span), seen func(pos int)) {
	limit := MaxLen(d.Lex)
	var walk func(pos int, reading []zhuyin.Syllable, cuts []int, cost float64, untoned, abbrev, corrected bool)
	walk = func(pos int, reading []zhuyin.Syllable, cuts []int, cost float64, untoned, abbrev, corrected bool) {
		if len(reading) == limit {
			return
		}
		if pos >= len(lat) {
			if seen != nil {
				seen(-1)
			}
			return
		}
		if seen != nil {
			seen(pos)
		}
		// 寬列（簡拼展開的位置有上百個讀音）：先列出詞庫裡接得上的音節，不逐一試 HasPrefix。
		var next map[zhuyin.Syllable]bool
		if len(lat[pos]) > continuationRow {
			next = map[zhuyin.Syllable]bool{}
			if !lexicon.Continuations(d.Lex, reading, func(y zhuyin.Syllable) { next[y] = true }) {
				next = nil
			}
		}
		for _, h := range lat[pos] {
			end := pos + 1
			if h.Span > 1 {
				end = pos + h.Span
				if end > len(lat) {
					if seen != nil {
						seen(-1)
					}
					continue
				}
			}
			if d.MaxCost > 0 && cost+h.Cost > d.MaxCost {
				continue
			}
			if next != nil && !next[h.Syl] {
				continue
			}
			r := append(reading[:len(reading):len(reading)], h.Syl)
			if next == nil && !d.Lex.HasPrefix(r) {
				continue
			}
			c := cuts
			if h.Span > 1 || cuts != nil {
				if cuts == nil {
					c = make([]int, len(reading), len(reading)+1)
					for k := range c {
						c[k] = start + k + 1
					}
				}
				c = append(c[:len(c):len(c)], end)
			}
			u, a, x := untoned || h.Untoned, abbrev || h.Abbreviated, corrected || h.Corrected
			fn(span{r, end, c, cost + h.Cost, u, a, x})
			walk(end, r, c, cost+h.Cost, u, a, x)
		}
	}
	walk(start, nil, nil, 0, false, false, false)
}

// Fits 回報 word 有沒有詞庫讀音放得進 lat 從 start 起的音節，以及那筆是不是自動修正讀音。
// 只算使用者打的音（含沒打聲調、簡拼的展開），不算近音：同候選裡不帶 Matched 的詞。
// 不評分，給不開候選、直接固定詞的手選用（session.fixWord）。
func (d *Decoder) Fits(lat input.Lattice, start int, word string) (fits, correction bool) {
	return d.FitsIn(lat, start, start+utf8.RuneCountInString(word), word)
}

// FitsIn 同 Fits，但 word 佔的是位置 [start, end)：按鍵圖（自動切字）的位置是鍵，詞的字數跟位置數不同，
// 只要有一種切法讓詞庫讀音剛好走到 end 就算放得進。
func (d *Decoder) FitsIn(lat input.Lattice, start, end int, word string) (fits, correction bool) {
	length := utf8.RuneCountInString(word)
	if start < 0 || length == 0 || end > len(lat) || end <= start {
		return false, false
	}
	d.spans(lat[:end], start, func(sp span) {
		reading := sp.reading
		if fits && !correction || len(reading) != length || sp.end != end || matchedReading(lat, start, reading, sp.cuts) != "" {
			return
		}
		for _, e := range d.Lex.Exact(reading) {
			if e.Surface == word && (!fits || correction && !e.Correction) {
				fits, correction = true, e.Correction // 有一般讀音就不算修正
			}
		}
	})
	return fits, correction
}

type state struct {
	last  string
	score float64
	path  *pathNode
}

// pathNode 是前向狀態走過的路徑，由最後一段往回接：延伸一段只配置一個節點，前面的部分共用，選出最佳路徑後才攤平
// （segs）。一直複製整條 []Seg 的話，配置量跟路徑長度的平方成正比。
type pathNode struct {
	seg  Seg
	prev *pathNode
}

func (p *pathNode) push(seg Seg) *pathNode { return &pathNode{seg, p} }

// last 是路徑的最後一段；空路徑是 nil。
func (p *pathNode) last() *Seg {
	if p == nil {
		return nil
	}
	return &p.seg
}

func (p *pathNode) segs() []Seg {
	n := 0
	for q := p; q != nil; q = q.prev {
		n++
	}
	if n == 0 {
		return nil
	}
	out := make([]Seg, n)
	for q := p; q != nil; q = q.prev {
		n--
		out[n] = q.seg
	}
	return out
}

// lastOf 是 segs 的最後一段；空的時候是 nil。
func lastOf(segs []Seg) *Seg {
	if len(segs) == 0 {
		return nil
	}
	return &segs[len(segs)-1]
}

// allowed 判斷詞 w 佔 [i, j) 是否符合鎖定。鎖定限制的是「那幾個音節的字」，不是斷詞：
// 詞完整包住鎖定區間、且該位置的字與鎖定相同時也允許（選「今天」的「天」不會把「今」拆成單字重猜）。
// 被字面文字切開的暫時邊界（LiteralBoundary）仍要求剛好是那個詞。
// cuts 是詞的讀音每個音節結束的位置（nil 時每個音節佔一個位置）：按鍵圖裡鎖定的兩端必須同時是這個詞的音節邊界。
func allowed(locks map[int]Lock, i, j int, w string, cuts []int) bool {
	var runes []rune
	for s, l := range locks {
		if s < i || s >= j || s == i && l.Word == w && l.Len == j-i {
			continue
		}
		if !l.Segmentation && l.LiteralBoundary == 0 && s+l.Len <= j {
			if runes == nil {
				runes = []rune(w)
			}
			a, okA := charAt(i, cuts, s)
			b, okB := charAt(i, cuts, s+l.Len)
			if okA && okB && len(runes) == syllables(i, j, cuts) && utf8.RuneCountInString(l.Word) == b-a && string(runes[a:b]) == l.Word {
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
	cuts *[]int // 每個音節結束的位置；每個音節佔一個位置時是 nil（見 Seg.Cuts）
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
	return d.finish(lat, locks, prefix, d.forward(lat, locks), nil)
}

// finish 從前向狀態挑出最佳路徑；prefix 時尾端可以是更長的詞的前綴。dead 非 nil 時，回報 true 的起點確定沒有走到
// 音節格終點的讀音（增量搜尋已經知道），不必列舉尾端詞條；結果跟列舉了相同。
func (d *Decoder) finish(lat input.Lattice, locks map[int]Lock, prefix bool, best [][]state, dead func(int) bool) ([]Seg, float64) {
	n := len(lat)
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
			if lockedIn(locks, i, n) || len(best[i]) == 0 || dead != nil && dead(i) {
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
			var one [1]tailGroup
			groups := tailGroups(one[:0], cands, i, n)
			for _, st := range sts {
				for _, g := range groups {
					for _, t := range d.group(g.cands, g.k, st.last, d.Continue) {
						if !t.longer {
							continue
						}
						seg := Seg{Word: t.display, Start: i, End: n, Partial: true, Matched: partialMatched(lat, i, t, deref(g.cuts)), Cuts: g.cuts}
						sc := d.repeatScore(lat, locks, st.path.last(), i, n, t.display, math.Log(d.evidenceMass(i, n, st.last, t.display, t.mass)), deref(g.cuts))
						consider(state{t.display, st.score + sc, st.path.push(seg)})
					}
				}
			}
		}
	}
	if winner == nil {
		return nil, math.Inf(-1)
	}
	return winner.path.segs(), winner.score
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
				sc := st.score + d.repeatScore(lat, locks, st.path.last(), i, a.j, a.e.Surface, d.arcLog(i, a.j, st.last, a.e, a.cost), deref(a.cuts))
				seg := Seg{Word: a.e.Surface, Start: i, End: a.j, Correction: a.e.Correction, Matched: segMatched(lat, i, a.e, a.cost, deref(a.cuts)), Cuts: a.cuts}
				put(a.j, state{a.e.Surface, sc, st.path.push(seg)})
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
	cuts *[]int // 已打部分每個音節結束的位置；每個音節佔一個位置時是 nil（見 Seg.Cuts）
}

// typed 是從 start 起、走到 end 的這筆尾端詞條已經打了幾個音節。
func (t tailEntry) typed(start, end int) int { return syllables(start, end, deref(t.cuts)) }

// tailEntries 回傳「讀音以 lat[i:] 開頭」的詞：系統詞取依先驗排序的前 M 個（快取），
// 個人片語全部加入（不快取，學習立即生效）。按鍵圖有好幾種切法走到終點，各自的 k、cuts 不同。
func (d *Decoder) tailEntries(lat input.Lattice, i int) []tailEntry {
	var out []tailEntry
	d.spans(lat, i, func(sp span) {
		if sp.end != len(lat) {
			return
		}
		reading, cost, cuts := sp.reading, sp.cost, ref(sp.cuts)
		sys, adjust, personal := d.Lex, func(es []lexicon.Entry) []lexicon.Entry { return es }, []lexicon.Entry(nil)
		if l, ok := d.Lex.(Layered); ok {
			sys, adjust, personal = l.System(), l.Adjust, l.PersonalPrefixed(reading)
		}
		for _, e := range adjust(d.topPrefixed(sys, reading)) {
			out = append(out, tailEntry{e, cost, cuts})
		}
		for _, e := range personal {
			out = append(out, tailEntry{e, cost, cuts})
		}
	})
	return out
}

// tailGroup 是尾端從同一個起點、以同一種切法走到音節格終點的詞條：k 是已經打了的音節數，cuts 是切法。
// 一般的音節格只有一組（k 是剩下的位置數、cuts 是 nil），順序跟 tailEntries 相同。
type tailGroup struct {
	k     int
	cuts  *[]int
	cands []tailEntry
}

// tailGroups 把從 start 起走到 end 的尾端詞條依切法分組，接在 buf 後面。沒有任何一筆佔多個位置（一般的音節格）時
// 不重新分組，整份就是一組。
func tailGroups(buf []tailGroup, cands []tailEntry, start, end int) []tailGroup {
	plain := true
	for i := range cands {
		if cands[i].cuts != nil {
			plain = false
			break
		}
	}
	if plain {
		return append(buf, tailGroup{k: end - start, cands: cands})
	}
	out := buf
	for _, c := range cands {
		k := c.typed(start, end)
		i := slices.IndexFunc(out, func(g tailGroup) bool { return g.k == k && slices.Equal(deref(g.cuts), deref(c.cuts)) })
		if i < 0 {
			i = len(out)
			out = append(out, tailGroup{k: k, cuts: c.cuts})
		}
		out[i].cands = append(out[i].cands, c)
	}
	return out
}

// PriorVersion 是選配介面（比照 MassVersion）：評分源回報 Prior 的版本，同一個版本裡每個詞的 Prior 都不變。
// owner 必須可比較（通常是指標），否則比對時會 panic。personal.Model 的 Prior 只隨自訂詞彙改變，學習不影響它。
type PriorVersion interface {
	PriorVersion() (owner any, version uint64)
}

// prefixKey 是 topPrefixed 快取建立時的條件；不同就整份重算。
type prefixKey struct {
	topM    int
	owner   any
	version uint64
	mass    bool // 版本來自 MassVersion（評分源沒有 PriorVersion）
	time    int64
}

// prefixVersion 讓 topPrefixed 的快取與上限、清除時機無關（#307）：前 M 名依 Prior 排序，Prior 會隨自訂詞彙改變
// （自訂詞的頻率下限）。評分源報 PriorVersion 時照它；沒有時看個人資料版本（MassVersion），因為沒有 Prior 的評分源
// 改用含個人計數的 Prob 排序。兩者都不報的評分源視為不變。
func (d *Decoder) prefixVersion() {
	key := prefixKey{topM: d.TopM}
	if v, ok := d.Score.(PriorVersion); ok {
		key.owner, key.version = v.PriorVersion()
	} else if v, ok := d.Score.(MassVersion); ok {
		gen, at := v.MassVersion()
		key.version, key.time, key.mass = uint64(gen), at, true
	}
	if (d.cache != nil || d.cacheOld != nil) && key != d.cacheKey {
		d.cache, d.cacheOld = nil, nil
	}
	d.cacheKey = key
}

func (d *Decoder) topPrefixed(sys lexicon.Source, reading []zhuyin.Syllable) []lexicon.Entry {
	key := zhuyin.Key(reading)
	d.prefixVersion()
	if es, ok := d.cache[key]; ok {
		return es
	}
	if es, ok := d.cacheOld[key]; ok {
		d.cachePut(key, es)
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
	d.cachePut(key, top)
	return top
}

// cachePut 把讀音 key 的前 M 名放進新的一代（見 Decoder.cache）；兩代加起來不超過 PrefixCacheKeys。
func (d *Decoder) cachePut(key string, top []lexicon.Entry) {
	limit := d.PrefixCacheKeys
	if limit == 0 {
		limit = maxCacheKeys
	}
	if limit < 0 {
		d.cache, d.cacheOld = nil, nil
		return
	}
	if limit < 2 {
		d.cacheOld = nil
	}
	if d.cache == nil || len(d.cache) >= max(1, limit/2) {
		if limit >= 2 {
			d.cacheOld = d.cache
		}
		d.cache = map[string][]lexicon.Entry{}
	}
	d.cache[key] = top
}

// partialMatched 是尾端前綴段的 Matched：取最佳詞條讀音中對應已輸入音節的部分。cuts 是已輸入部分的切法（見 Seg.Cuts）。
func partialMatched(lat input.Lattice, start int, t *tail, cuts []int) string {
	k := syllables(start, len(lat), cuts)
	if t.bestCost <= 0 || len(t.best.Reading) < k {
		return ""
	}
	return matchedReading(lat, start, t.best.Reading[:k], cuts)
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
	// 按鍵圖上每種切法打了的音節數不同，逐筆看；一般的音節格每筆都是剩下的位置數。
	if typed := len(lat) - start; lat.Width() == 1 && (typed < 2 || typed >= MaxLen(d.Lex)) {
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
		if k := c.typed(start, len(lat)); k < 2 || k >= MaxLen(d.Lex) || len(c.e.Reading) <= k || c.e.Correction || c.e.Rare {
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
	k := last.Syllables()
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
		if c.typed(last.Start, len(lat)) != k || !slices.Equal(deref(c.cuts), last.Ends()) || string([]rune(c.e.Surface)[:k]) != last.Word {
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
	// 每個位置的詞圖只列舉一次，與整句解碼用同一組弧（wordArcs）：沒打聲調、簡拼的位置各依無上下文機率
	// 留前幾名。右側的後綴動態規劃不設 beam，全部列舉時打到十幾個音節，句首一個詞長的查詢比整句解碼
	// 貴一個數量級，手機上要十幾秒（#305）；打了聲調的位置不裁，結果不變。
	arcs := make([][]arc, len(lat))
	for at := end; at < len(lat); at++ {
		arcs[at] = d.wordArcs(lat, at, kept)
	}
	type key struct {
		at   int
		left string
	}
	cache := map[key]float64{}
	// 無上下文的前一個詞對每一條弧給出相同的分數（同 frontier 的前提），後綴最佳分數也就相同：
	// 共用一格快取，沒有上下文的候選不必各自把右側重算一次（#305）。
	ctx, _ := d.Score.(Contextual)
	var suffix func(int, string) float64
	suffix = func(at int, left string) float64 {
		if at == len(lat) {
			return 0
		}
		k := key{at, left}
		if ctx != nil && d.Evidence == nil && !ctx.HasContext(left) {
			k.left = "\x00"
		}
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
	limit := MaxLen(d.Lex) * lat.Width() // 按鍵圖的一個音節最多佔 Width 個位置
	for s := max(0, end-limit); s <= start; s++ {
		if len(fw[s]) == 0 {
			continue
		}
		states := d.frontier(fw[s])
		d.spans(lat, s, func(sp span) {
			reading, cost, j := sp.reading, sp.cost, sp.end
			if j < end || s == start && j == end {
				return
			}
			a, okA := charAt(s, sp.cuts, start)
			b, okB := charAt(s, sp.cuts, end)
			if !okA || !okB {
				return // 候選範圍的兩端不是這個讀音的音節邊界
			}
			for _, e := range d.Lex.Exact(reading) {
				runes := []rune(e.Surface)
				if len(runes) != len(reading) || !allowed(kept, s, j, e.Surface, sp.cuts) {
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
				k := innerKey(string(runes[a:b]), reading[a:b])
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
	d.spans(lat[:start+length], start, func(sp span) {
		reading, cost := sp.reading, sp.cost
		if sp.end != start+length || d.OnlyCosted && (cost <= 0 || matchedReading(lat, start, reading, sp.cuts) == "") {
			return // 只列近音時，簡拼展開的成本不算
		}
		for _, e := range d.Lex.Exact(reading) {
			if accept != nil && !accept(e) {
				continue
			}
			if d.MaxCandidates > 0 {
				pool = append(pool, tailEntry{e: e, cost: cost})
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
