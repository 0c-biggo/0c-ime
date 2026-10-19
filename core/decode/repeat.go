package decode

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// repeatMargin 是非詞疊字至少要輸給同讀音詞庫詞的幅度（自然對數，e 倍）。與小麥注音「長詞彙權重
// 係數 2.7」同一量級；只讓兩者不會近乎同分，不是校準過的參數。
const repeatMargin = 1.0

// SystemBigram 是 Scorer 的選用擴充：系統語言模型中 left 後接 w 實際觀察到的 bigram 項，不含內插時退回
// 單字頻率的部分（personal.Model 是 μ·P_bi(w|left)，SystemProb 減去它就是退回的部分）。
type SystemBigram interface {
	SystemBigram(left, w string) float64
}

// PairObserved 是 Scorer 的選用擴充：使用者是否真的提交過 left 後面接 w。
// 非詞疊字的上限只約束系統語言模型，使用者自己用過的組合照個人學習排序（#139）。
type PairObserved interface {
	ObservedPair(left, w string) bool
}

// capRepeat 處理詞庫裡沒有、但同一段讀音有詞庫詞的疊字 XX（ㄣ ㄣ 的「恩恩」，詞庫有「嗯嗯」）。
// 回傳這一段（start 到 end）調整後的對數分數，以及疊字是否受上限約束（神經選字不得把它換回首選，
// 見 Hypothesis.NonWordRepeat）。兩種來路：
//
//   - 兩個相同的單字「恩」「恩」。系統語言模型的自身 bigram（恩→恩）多半來自語料把沒收錄的疊字
//     斷成兩個單字，加上內插退回的單字頻率（「是」很常見，「是是」就跟著高），常讓這條路徑贏過
//     詞頻估得很低的詞庫詞（嗯嗯、試試、沙沙）。第二個字的轉移機率最多讓 P(X)·P(X|X) 等於
//     P(詞)／e；三者都用不含前文的系統先驗，所以前文支持第一個字時（傳來→陣陣）照樣有利，
//     只是疊字本身不能比詞庫詞更像一個詞。上限只看位置與 X，維特比的狀態合併仍然精確。
//   - 組字尾端「恩恩怨怨」這類 AABB 長詞的前兩字：還沒打完時顯示的 AA 本身不是詞，就此提交
//     也不是詞；最多讓它比從同一前詞走完整的詞庫詞低 repeatMargin。打到第三個音節以後顯示的
//     不再是 AA，照原本的前綴規則競爭。
//
// 上限只在它能讓詞庫詞勝出時才有意義（#140）：詞庫詞不看前文就輸給這兩個音節其他兩個單字的讀法時
// （ㄎㄜ ㄎㄜ 的磕磕只有補足詞頻，輸給「顆顆」「柯科」），不設上限，免得疊字讓給既不是詞、也不是疊字的
// 組合。兩個單字時另看詞庫詞的字：沒有一個詞庫詞的字跟 X 差不多常用（嘓嘓對郭郭）時，上限只拿掉內插
// 退回單字頻率灌進來的部分，語料實際寫過的 X→X 照算，此時不算被上限壓過（見 contest）。
//
// 原本就在上限以下的疊字分數不變；只有詞庫詞的字跟 X 差不多常用、而且詞庫詞兩字相黏的程度明顯高過
// X→X 時（contest 的 lexical），仍算受上限約束。詞庫詞的詞頻夠高、不必壓分就勝出時（新詞庫的嗯嗯），
// 恩恩還是同一種把詞寫成常用字的非詞疊字，不能因為詞庫詞更強反而讓神經選字換回去（#139）；
// 詞庫詞勝出只是因為它的字比 X 常用（每每對美美、單單對丹丹、下下對嚇嚇）時，X→X 本身相黏的程度
// 不輸給詞庫詞，疊字可能是另一個詞，照常交給神經選字。
//
// 疊字本身是詞庫（或個人）詞、這段沒有其他詞、鎖定排除了那些詞、或使用者提交過 X 後接 X 時不改分數。
// segs 是走到 start 為止的路徑。
func (d *Decoder) capRepeat(lat input.Lattice, locks map[int]Lock, segs []Seg, start, end int, word string, score float64) (float64, bool) {
	var at int
	var single, left string
	first, size := utf8.DecodeRuneInString(word)
	switch {
	case size == len(word) && end == start+1 && len(segs) > 0:
		prev := &segs[len(segs)-1]
		if prev.Word != word || prev.Partial || prev.Start != start-1 || prev.End != start {
			return score, false
		}
		at, single = start-1, word
	case end == start+2 && size < len(word) && word[size:] == word[:size] && first != utf8.RuneError:
		at, single, left = start, word[:size], d.InitialLeft()
		if len(segs) > 0 {
			left = segs[len(segs)-1].Word
		}
	default:
		return score, false
	}
	if o, ok := d.Score.(PairObserved); ok && o.ObservedPair(single, single) {
		return score, false
	}
	r := d.repeatWords(lat, locks, at, start, end, single)
	unit, word2, isWord := r.unit, r.word2, r.isWord
	if !isWord && at == start {
		word2 = d.repeatWordFrom(r, at, end, left)
	}
	if isWord || !r.wins || math.IsInf(word2, -1) || at < start && !(unit > 0) {
		return score, false
	}
	limit := word2 - repeatMargin
	if at < start {
		limit -= math.Log(unit)
	}
	if score <= limit {
		return score, r.lexical
	}
	if at < start && !r.common {
		// 詞庫詞的字比 X 罕用得多（嘓嘓對郭郭）：只拿掉內插退回單字頻率的部分，語料實際寫過的
		// X→X 照算；這時疊字不算被上限壓過，神經選字照常可以選它。
		if floor := score + math.Log(r.seen); floor >= limit {
			return floor, false
		}
	}
	return limit, true
}

// capRepeat 的詞庫列舉以 repeatWords 快取（#139 之後連續同音節的句子變慢：N-best 的每個前詞狀態、
// 每一列都以同一段讀音重新列舉詞庫）。同一次搜尋的音節格與鎖定不變，列舉結果只取決於位置與 X；
// XX 本身成段時的詞庫詞分數另依前詞快取。每次搜尋開始時由 resetRepeats 清空。結果與不快取逐位元相同：
// 列舉與取最大值的順序都不變。
type repeatKey struct {
	at, start, end int
	single         string
}

type repeatWords struct {
	unit, word2 float64
	isWord      bool
	wins        bool    // 同讀音詞庫詞不看前文時勝過兩個單字的其他讀法（見 contest）
	common      bool    // 有個同讀音詞庫詞的字跟 X 差不多常用（見 contest）
	lexical     bool    // common 的詞庫詞之中，有個兩字相黏的程度明顯高過 X→X（見 contest）
	seen        float64 // 兩個單字時，X→X 的系統機率中實際觀察到的 bigram 所占的比例
	arcs        []arc   // at == start 時同讀音的詞庫詞（依列舉順序），word2 要依前詞算
	from        map[string]float64
}

func (d *Decoder) resetRepeats() {
	clear(d.repeats)
	clear(d.repeatSpans)
	clear(d.contests)
}

// repeatWords 列舉 [at, end) 的詞庫讀音：unit 是第一個字的先驗（只用於兩個單字），word2 是同讀音
// 詞庫詞的最佳對數分數（兩個單字的來路；XX 成段時見 repeatWordFrom），isWord 表示疊字本身就是詞。
func (d *Decoder) repeatWords(lat input.Lattice, locks map[int]Lock, at, start, end int, single string) *repeatWords {
	k := repeatKey{at, start, end, single}
	if r, ok := d.repeats[k]; ok {
		return r
	}
	prior := func(w string) float64 { return d.Score.Prob(lm.BOS, w) }
	if p, ok := d.Score.(Prior); ok {
		prior = p.Prior
	}
	pair := single + single
	r := &repeatWords{word2: math.Inf(-1)}
	if d.repeats == nil {
		d.repeats = map[repeatKey]*repeatWords{}
	}
	word := math.Inf(-1) // 同讀音詞庫詞不含前文的最佳對數先驗
	var words []string
	var logs []float64 // words 各自不含前文的對數先驗
	for _, c := range d.repeatSpan(lat, at, end) {
		e := c.e
		switch {
		case c.n == 1 && at < start && e.Surface == single:
			r.unit = max(r.unit, prior(e.Surface)*e.PRW*math.Exp(-c.cost))
		case c.n != end-at:
		case e.Surface == pair:
			r.isWord = true
		case !e.Correction && allowed(locks, at, end, e.Surface):
			lp := math.Log(prior(e.Surface)*e.PRW) - c.cost
			word = max(word, lp)
			words, logs = append(words, e.Surface), append(logs, lp)
			if at < start {
				r.word2 = word
			} else {
				r.arcs = append(r.arcs, arc{end, e, c.cost})
			}
		}
	}
	if r.isWord || math.IsInf(word, -1) {
		d.repeats[k] = r
		return r
	}
	r.wins, r.common, r.lexical = d.contest(lat, locks, at, single, word, words, logs, prior)
	if b, ok := d.Score.(SystemBigram); ok && at < start {
		if p := d.systemProb(single, single); p > 0 {
			r.seen = min(b.SystemBigram(single, single)/p, 1)
		}
	}
	d.repeats[k] = r
	return r
}

// contest 比較 [at, at+2) 這兩個音節不看前文的讀法（先驗與轉移只用系統語言模型，不含個人資料）。
// word 是同讀音詞庫詞 words 的最佳對數先驗。
//
// wins：詞庫詞勝過其他「兩個單字」的讀法 P(A)·P(B|A)（AB 不是 XX 本身）。兩個單字拼出的字面正好是
// 詞庫詞時（磕＋磕＝磕磕）算是那個詞，以兩者較高的分數比較。上限的目的是讓詞庫詞贏過非詞疊字；
// 詞庫詞本身就輸給別的讀法時（ㄎㄜ ㄎㄜ 的磕磕只有補足詞頻，輸給另一個疊字「顆顆」與有 bigram 的
// 「柯科」；ㄋㄧˊ ㄋㄧˊ 的呢呢輸給「尼尼」），壓低疊字也不會讓詞庫詞勝出，只會把疊字讓給一個
// 既不是詞、也不是疊字的組合（柯科、倪妮、季繼），這時不設上限（#140）。
//
// common：有個詞庫詞的兩個字在各自位置上（含讀音權重）都不比 X 罕用超過 e 倍，也就是打這個音時
// 使用者要那兩個字的機會跟要 X 差不多（嗯嗯對恩恩、試試對是是不算：「是」常用得多）。
//
// lexical：common 的詞庫詞 YZ 之中，有個詞的兩字相黏程度 log P(YZ)/(P(Y)·P(Z)) 比 X→X 的
// log P(X|X)/P(X) 高出 repeatMargin 以上（都是不含前文的系統機率；讀音權重與音節格成本在兩邊各自
// 消去）。比的是扣掉字頻之後的成詞證據。0.2.0 詞庫裡嗯嗯比恩恩更像一個詞（7.0 對 3.7），每每卻沒有
// 比美美更像（2.9 對 2.3），單單、下下更不如丹丹、嚇嚇。
func (d *Decoder) contest(lat input.Lattice, locks map[int]Lock, at int, single string, word float64, words []string, logs []float64, prior func(string) float64) (wins, common, lexical bool) {
	c := d.spanContest(lat, locks, at, word, words, prior)
	x1, x2 := c.best(false, single), c.best(true, single)
	assoc := math.Inf(1) // X→X 的成詞程度：log P(X|X)/P(X)
	if p, q := d.systemProb(single, single), prior(single); q > 0 {
		assoc = math.Log(p / q)
	}
	for i, w := range words {
		r := []rune(w)
		if len(r) != 2 {
			continue
		}
		a, b := c.best(false, string(r[0])), c.best(true, string(r[1]))
		if a >= x1-repeatMargin && b >= x2-repeatMargin {
			common = true
			lexical = lexical || logs[i]-a-b >= assoc+repeatMargin
		}
	}
	return len(c.beat) == 0 || len(c.beat) == 1 && c.beat[0] == single+single, common, lexical
}

// spanContest 是 contest 中與 X 無關的部分，同一次搜尋內每個位置只算一次（#140：連續同音節的位置
// 每個 X 都重算兩層迴圈，ㄐㄧˋ ㄐㄧˋ 這類常用音節每鍵多花上百毫秒）。beat 是分數不低於詞庫詞字面的
// 非詞庫讀法，找到兩個不同的字面就停：排除任何一個 XX 之後仍有別的讀法勝過詞庫詞。
type spanContest struct {
	firsts, seconds []spanEntry
	p1, p2          []float64 // firsts、seconds 各自不含前文的對數先驗（含讀音權重與音節格成本）
	beat            []string
}

func (c *spanContest) best(second bool, w string) float64 {
	cs, ps := c.firsts, c.p1
	if second {
		cs, ps = c.seconds, c.p2
	}
	v := math.Inf(-1)
	for i, e := range cs {
		if e.e.Surface == w {
			v = max(v, ps[i])
		}
	}
	return v
}

func (d *Decoder) spanContest(lat input.Lattice, locks map[int]Lock, at int, word float64, words []string, prior func(string) float64) *spanContest {
	if c, ok := d.contests[at]; ok {
		return c
	}
	singles := func(pos int) ([]spanEntry, []float64) {
		var out []spanEntry
		var ps []float64
		for _, c := range d.repeatSpan(lat, pos, at+2) {
			if c.n == 1 && !c.e.Correction && allowed(locks, pos, pos+1, c.e.Surface) {
				out = append(out, c)
				ps = append(ps, math.Log(prior(c.e.Surface)*c.e.PRW)-c.cost)
			}
		}
		return out, ps
	}
	c := &spanContest{}
	c.firsts, c.p1 = singles(at)
	c.seconds, c.p2 = singles(at + 1)
	path := func(i, j int) float64 {
		a, b := c.firsts[i].e, c.seconds[j]
		return c.p1[i] + math.Log(d.systemProb(a.Surface, b.e.Surface)*b.e.PRW) - b.cost
	}
	isWord := func(a, b string) bool {
		for _, w := range words {
			if len(w) == len(a)+len(b) && w[:len(a)] == a && w[len(a):] == b {
				return true
			}
		}
		return false
	}
	// 詞庫詞的字面：詞本身，或兩個單字拼出同一個字面（磕＋磕＝磕磕），取較高者。
	lex := word
	for _, w := range words {
		for i, a := range c.firsts {
			if !strings.HasPrefix(w, a.e.Surface) {
				continue
			}
			for j, b := range c.seconds {
				if len(a.e.Surface)+len(b.e.Surface) == len(w) && strings.HasSuffix(w, b.e.Surface) {
					lex = max(lex, path(i, j))
				}
			}
		}
	}
	for i, a := range c.firsts {
		if c.p1[i] < lex { // P(B|A) ≤ 1：這條讀法不會超過詞庫詞
			continue
		}
		for j, b := range c.seconds {
			if path(i, j) < lex || isWord(a.e.Surface, b.e.Surface) {
				continue
			}
			if text := a.e.Surface + b.e.Surface; !slices.Contains(c.beat, text) {
				if c.beat = append(c.beat, text); len(c.beat) == 2 {
					break
				}
			}
		}
		if len(c.beat) == 2 {
			break
		}
	}
	if d.contests == nil {
		d.contests = map[int]*spanContest{}
	}
	d.contests[at] = c
	return c
}

// systemProb 是不含個人資料的 P(w|left)；評分器沒有分開系統機率時用 Prob。
func (d *Decoder) systemProb(left, w string) float64 {
	if e, ok := d.Score.(Evidenced); ok {
		return e.SystemProb(left, w)
	}
	return d.Score.Prob(left, w)
}

// repeatSpan 列舉 [at, end) 起點的詞庫讀音（依 spans 與 Exact 的順序），只留單音節與剛好到 end 的詞條；
// 與 X 無關，同一次搜尋內每個位置只列舉一次。
func (d *Decoder) repeatSpan(lat input.Lattice, at, end int) []spanEntry {
	k := [2]int{at, end}
	if es, ok := d.repeatSpans[k]; ok {
		return es
	}
	var es []spanEntry
	d.spans(lat[:end], at, func(reading []zhuyin.Syllable, cost float64) {
		if n := len(reading); n == 1 || n == end-at {
			for _, e := range d.Lex.Exact(reading) {
				es = append(es, spanEntry{n, e, cost})
			}
		}
	})
	if d.repeatSpans == nil {
		d.repeatSpans = map[[2]int][]spanEntry{}
	}
	d.repeatSpans[k] = es
	return es
}

type spanEntry struct {
	n    int // 讀音音節數
	e    lexicon.Entry
	cost float64
}

// repeatWordFrom 是 XX 成段時，從前詞 left 走同讀音詞庫詞的最佳對數分數。
func (d *Decoder) repeatWordFrom(r *repeatWords, at, end int, left string) float64 {
	if v, ok := r.from[left]; ok {
		return v
	}
	word2 := math.Inf(-1)
	for _, a := range r.arcs {
		word2 = max(word2, d.arcLog(at, end, left, a.e, a.cost))
	}
	if r.from == nil {
		r.from = map[string]float64{}
	}
	r.from[left] = word2
	return word2
}

// repeatScore 是 capRepeat 的分數，供只需要分數的搜尋使用。
func (d *Decoder) repeatScore(lat input.Lattice, locks map[int]Lock, segs []Seg, start, end int, word string, score float64) float64 {
	score, _ = d.capRepeat(lat, locks, segs, start, end, word, score)
	return score
}
