package decode

import (
	"math"
	"sort"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Hypothesis 是完整顯示字串及其最高分路徑；不是逐位置候選的拼接。
// 同字串的不同分詞／讀音只保留最高分，不把重複路徑算成召回收益。
type Hypothesis struct {
	Text     string  `json:"text"`
	LogScore float64 `json:"log_score"`
	Segments []Seg   `json:"segments"`
	// NonWordRepeat：受非詞疊字的上限約束（分數被壓低過，或原本就在上限以下而詞庫詞成詞的證據明顯較強，
	// 見 capRepeat）。不送給評分器。
	NonWordRepeat bool `json:"-"`
	// WeakPrefix：尾端是只打了一個音節的長詞開頭，顯示的字本身不是這個讀音的字，而這個讀音有自己的字
	// （見 weakPrefix）。不送給評分器。
	WeakPrefix bool `json:"-"`
}

type SearchStats struct {
	ScoredExtensions int `json:"scored_extensions"`
	RetainedPrefixes int `json:"retained_prefixes"`
	PrunedContexts   int `json:"pruned_contexts"`
}

type hypotheses struct {
	last string
	rows []Hypothesis
}

// keep 依分數穩定排序。狀態相同且文字相同時，較差路徑的所有未來延伸都被支配。
// 每個狀態保留 K 個不同文字，才足以產生 K 個不同的最終文字。
func keep(rows []Hypothesis, h Hypothesis, k int) []Hypothesis {
	if math.IsNaN(h.LogScore) || math.IsInf(h.LogScore, 0) {
		return rows
	}
	for i, old := range rows {
		if old.Text == h.Text {
			if old.LogScore >= h.LogScore {
				return rows
			}
			rows = append(rows[:i], rows[i+1:]...)
			break
		}
	}
	i := sort.Search(len(rows), func(i int) bool { return rows[i].LogScore < h.LogScore })
	if i >= k {
		return rows
	}
	rows = append(rows, Hypothesis{})
	copy(rows[i+1:], rows[i:])
	rows[i] = h
	return rows[:min(k, len(rows))]
}

func extend(h Hypothesis, s Seg, score float64) Hypothesis {
	path := append(h.Segments[:len(h.Segments):len(h.Segments)], s)
	return Hypothesis{Text: h.Text + s.Word, LogScore: h.LogScore + score, Segments: path, NonWordRepeat: h.NonWordRepeat, WeakPrefix: h.WeakPrefix}
}

// offer 先用分數界排除不可能進榜的延伸，避免為落選路徑配置整份 Seg slice。
func offer(rows []Hypothesis, h Hypothesis, seg Seg, delta float64, capped, weak bool, k int) []Hypothesis {
	score := h.LogScore + delta
	if math.IsNaN(score) || math.IsInf(score, 0) || len(rows) >= k && score <= rows[len(rows)-1].LogScore {
		return rows
	}
	text := h.Text + seg.Word
	for _, old := range rows {
		if old.Text == text && old.LogScore >= score {
			return rows
		}
	}
	next := extend(h, seg, delta)
	next.NonWordRepeat = next.NonWordRepeat || capped
	next.WeakPrefix = next.WeakPrefix || weak
	return keep(rows, next, k)
}

// tailChars 是音節格最後一個位置（各個假設）自己的字（不含修正讀音），供 weakPrefix 判斷：
// 尾端前綴段 [n-1, n) 只是「打了一個音節的長詞開頭」，顯示的字不在其中、而其中有字（ㄟ 的「工」只出現在
// 「工口 ㄟ ㄌㄨㄛˊ」，ㄟ 自己有「欸」）。就此停下時畫面上是讀音不對的字，只有一個音節對上長詞也不足以
// 說明使用者要的是它。統計分數本來就讓它與完整的字依機率競爭（突然的「突」、諷刺的「諷」照樣會贏）；
// 這個標記只讓只看文字的評分器不能把完整的字換成它（#107）。打了兩個以上音節的前綴（跆拳→跆拳道）不算。
func (d *Decoder) tailChars(lat input.Lattice) map[string]bool {
	own := map[string]bool{}
	for _, h := range lat[len(lat)-1] {
		for _, e := range d.Lex.Exact([]zhuyin.Syllable{h.Syl}) {
			if !e.Correction {
				own[e.Surface] = true
			}
		}
	}
	return own
}

func weakPrefix(own map[string]bool, at, n int, display string) bool {
	return n-at == 1 && len(own) > 0 && !own[display]
}

// repeatArc 判斷詞面 w 的弧可能怎樣被 capRepeat 改分：單字（接在同一個單字後面時）或兩個相同的字；
// 其他弧不必逐列呼叫 capRepeat。每條弧只判斷一次，逐列只比對前一段（見 mayCap）。
type repeatArc int

const (
	noRepeat repeatArc = iota
	singleRepeat
	pairRepeat
)

func repeatKind(w string) repeatArc {
	_, size := utf8.DecodeRuneInString(w)
	switch {
	case size == len(w):
		return singleRepeat
	case 2*size == len(w) && w[size:] == w[:size]:
		return pairRepeat
	}
	return noRepeat
}

// mayCap：這一列接上詞面 w（起點 at）時是否要交給 capRepeat。單字只有前一段剛好是同一個字才可能是疊字。
func (k repeatArc) mayCap(segs []Seg, at int, w string) bool {
	switch k {
	case singleRepeat:
		if len(segs) == 0 {
			return false
		}
		prev := &segs[len(segs)-1]
		return prev.End == at && prev.Word == w
	case pairRepeat:
		return true
	}
	return false
}

func (d *Decoder) wordArcs(lat input.Lattice, at int, locks map[int]Lock) []arc {
	var arcs []arc
	var pooled map[int][]arc // 經過沒打聲調位置的弧（#150），依終點分組，最後各留前 maxUntonedArcs 個
	d.spans(lat, at, func(reading []zhuyin.Syllable, cost float64) {
		end := at + len(reading)
		pool := untonedSpan(lat, at, end) && !lockTouches(locks, at, end)
		for _, e := range d.Lex.Exact(reading) {
			if allowed(locks, at, end, e.Surface) {
				if pool {
					if pooled == nil {
						pooled = map[int][]arc{}
					}
					pooled[end] = append(pooled[end], arc{end, e, cost})
				} else {
					arcs = append(arcs, arc{end, e, cost})
				}
			}
		}
	})
	return append(arcs, d.topUntonedArcs(pooled)...)
}

// NBest 與 Best 共用弧分數、前綴彙總及詞界保護。供離線評測與明確啟用的有界神經重排；預設搜尋不變。
// Beam 限制的是前詞狀態數，不是文字路徑數；無上下文狀態也不能只留一條路徑。
// Beam=0 且 TopB 足夠大時，在既有 TopM 前綴詞池內產生精確的前 K 個文字。
// 有限 Beam／TopB 仍是近似；它們依各狀態的最佳分數選取，與 K 無關。
func (d *Decoder) NBest(lat input.Lattice, locks map[int]Lock, prefix bool, k int) ([]Hypothesis, SearchStats) {
	var stats SearchStats
	if k <= 0 || len(lat) == 0 {
		return nil, stats
	}
	d.prepareMass(lat)
	d.resetRepeats()
	n := len(lat)
	states := make([][]hypotheses, n+1)
	indices := make([]map[string]int, n+1)
	states[0] = []hypotheses{{last: d.InitialLeft(), rows: []Hypothesis{{}}}}
	put := func(at int, last string, h Hypothesis, seg Seg, delta float64, capped bool) {
		if math.IsNaN(h.LogScore+delta) || math.IsInf(h.LogScore+delta, 0) {
			return
		}
		if indices[at] == nil {
			indices[at] = map[string]int{}
		}
		idx, found := indices[at][last]
		if !found {
			idx = len(states[at])
			indices[at][last] = idx
			states[at] = append(states[at], hypotheses{last: last})
		}
		states[at][idx].rows = offer(states[at][idx].rows, h, seg, delta, capped, false, k)
	}
	for at := 0; at < n; at++ {
		if len(states[at]) == 0 {
			continue
		}
		arcs := d.wordArcs(lat, at, locks)
		var contextual, plain []int
		ctx, has := d.Score.(Contextual)
		for i, st := range states[at] {
			if !has || d.Evidence != nil || ctx.HasContext(st.last) {
				contextual = append(contextual, i)
			} else {
				plain = append(plain, i)
			}
		}
		// Best 不對未實作 Contextual 的 scorer 套 Beam；保持同一搜尋契約。
		if has && d.Beam > 0 && len(contextual) > d.Beam {
			sort.SliceStable(contextual, func(i, j int) bool {
				return states[at][contextual[i]].rows[0].LogScore > states[at][contextual[j]].rows[0].LogScore
			})
			stats.PrunedContexts += len(contextual) - d.Beam
			contextual = contextual[:d.Beam]
		}
		for _, idx := range append(contextual, plain...) {
			st := states[at][idx]
			for _, a := range arcs {
				score := d.arcLog(at, a.j, st.last, a.e, a.cost)
				seg := Seg{Word: a.e.Surface, Start: at, End: a.j, Correction: a.e.Correction, Matched: segMatched(lat, at, a.e, a.cost)}
				repeat := repeatKind(a.e.Surface)
				for _, h := range st.rows {
					stats.ScoredExtensions++
					delta, capped := score, false
					if repeat.mayCap(h.Segments, at, a.e.Surface) {
						delta, capped = d.capRepeat(lat, locks, h.Segments, at, a.j, a.e.Surface, score)
					}
					put(a.j, a.e.Surface, h, seg, delta, capped)
				}
			}
		}
	}
	var result []Hypothesis
	for _, st := range states[n] {
		for _, h := range st.rows {
			result = keep(result, h, k)
		}
	}
	if prefix {
		topB := d.TopB
		if topB <= 0 {
			topB = defaultTopB
		}
		own := d.tailChars(lat)
		for at := 0; at < n; at++ {
			if lockedIn(locks, at, n) || len(states[at]) == 0 {
				continue
			}
			entries := d.tailEntries(lat, at)
			order := append([]hypotheses(nil), states[at]...)
			sort.SliceStable(order, func(i, j int) bool { return order[i].rows[0].LogScore > order[j].rows[0].LogScore })
			for _, st := range order[:min(topB, len(order))] {
				for _, t := range d.group(entries, n-at, st.last, d.Continue) {
					if !t.longer {
						continue
					}
					score := math.Log(d.evidenceMass(at, n, st.last, t.display, t.mass))
					seg := Seg{Word: t.display, Start: at, End: n, Partial: true, Matched: partialMatched(lat, at, n, t)}
					weak, repeat := weakPrefix(own, at, n, t.display), repeatKind(t.display)
					for _, h := range st.rows {
						stats.ScoredExtensions++
						delta, capped := score, false
						if repeat.mayCap(h.Segments, at, t.display) {
							delta, capped = d.capRepeat(lat, locks, h.Segments, at, n, t.display, score)
						}
						result = offer(result, h, seg, delta, capped, weak, k)
					}
				}
			}
		}
	}
	for _, pos := range states {
		for _, st := range pos {
			stats.RetainedPrefixes += len(st.rows)
		}
	}
	return result, stats
}

type TargetAudit struct {
	SurfaceSegmentable bool        `json:"surface_segmentable"`
	ReadingReachable   bool        `json:"reading_reachable"`
	Best               *Hypothesis `json:"best_in_capped_graph,omitempty"`
}

// AuditTarget 以已知文字限制路徑，只用於錯誤分層，不把答案注入 NBest 候選。
// Best 不套 Beam/TopB，仍使用 TopM 前綴詞池；ReadingReachable 另外看未裁切的
// 詞庫前綴。這能區分讀音不可達、詞池裁切及後續搜尋／排名，而非把 N-best miss
// 一律當作缺詞。SurfaceSegmentable 指可由詞（含單字）組成，不保證有完整長詞。
func (d *Decoder) AuditTarget(lat input.Lattice, locks map[int]Lock, prefix bool, text string) TargetAudit {
	d.prepareMass(lat)
	d.resetRepeats()
	n := len(lat)
	runes := []rune(text)
	var out TargetAudit
	if len(runes) != n || n == 0 {
		return out
	}
	surface, reachable := make([]bool, n+1), make([]bool, n+1)
	surface[0], reachable[0] = true, true
	states := make([]map[string]Hypothesis, n+1)
	states[0] = map[string]Hypothesis{d.InitialLeft(): {}}
	put := func(at int, last string, h Hypothesis) {
		if math.IsNaN(h.LogScore) || math.IsInf(h.LogScore, 0) {
			return
		}
		if states[at] == nil {
			states[at] = map[string]Hypothesis{}
		}
		if old, ok := states[at][last]; !ok || h.LogScore > old.LogScore {
			states[at][last] = h
		}
	}
	consider := func(h Hypothesis) {
		if !math.IsInf(h.LogScore, 0) && !math.IsNaN(h.LogScore) && (out.Best == nil || h.LogScore > out.Best.LogScore) {
			out.Best = &h
		}
	}
	limit := MaxLen(d.Lex)
	for at := 0; at < n; at++ {
		for end := at + 1; end <= min(n, at+limit); end++ {
			if surface[at] && d.Lex.Has(string(runes[at:end])) {
				surface[end] = true
			}
		}
		// 按前詞文字排序，避免 map iteration 決定同分路徑的分詞。
		var lefts []string
		for left := range states[at] {
			lefts = append(lefts, left)
		}
		sort.Strings(lefts)
		for _, a := range d.wordArcs(lat, at, locks) {
			if a.e.Surface != string(runes[at:a.j]) {
				continue
			}
			if reachable[at] && a.e.PRW > 0 && !math.IsInf(a.cost, 0) {
				reachable[a.j] = true
			}
			for _, left := range lefts {
				h := states[at][left]
				put(a.j, a.e.Surface, extend(h, Seg{Word: a.e.Surface, Start: at, End: a.j, Correction: a.e.Correction, Matched: segMatched(lat, at, a.e, a.cost)}, d.repeatScore(lat, locks, h.Segments, at, a.j, a.e.Surface, d.arcLog(at, a.j, left, a.e, a.cost))))
			}
		}
		if !prefix || lockedIn(locks, at, n) {
			continue
		}
		if reachable[at] {
			d.spans(lat, at, func(reading []zhuyin.Syllable, cost float64) {
				if len(reading) != n-at || math.IsInf(cost, 0) {
					return
				}
				for _, e := range d.Lex.Prefixed(reading) {
					if len(e.Reading) > n-at && e.PRW > 0 && utf8.RuneCountInString(e.Surface) >= n-at && string([]rune(e.Surface)[:n-at]) == string(runes[at:]) {
						out.ReadingReachable = true
					}
				}
			})
		}
		entries := d.tailEntries(lat, at)
		for _, left := range lefts {
			for _, t := range d.group(entries, n-at, left, d.Continue) {
				if t.longer && t.display == string(runes[at:]) {
					h := states[at][left]
					consider(extend(h, Seg{Word: t.display, Start: at, End: n, Partial: true, Matched: partialMatched(lat, at, n, t)}, d.repeatScore(lat, locks, h.Segments, at, n, t.display, math.Log(d.evidenceMass(at, n, left, t.display, t.mass)))))
				}
			}
		}
	}
	var finals []string
	for left := range states[n] {
		finals = append(finals, left)
	}
	sort.Strings(finals)
	for _, left := range finals {
		consider(states[n][left])
	}
	out.SurfaceSegmentable = surface[n]
	out.ReadingReachable = out.ReadingReachable || reachable[n]
	return out
}
