package decode

import (
	"reflect"
	"slices"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Search 是逐鍵解碼的增量狀態（注音九宮格自動切字），由呼叫端（session）保存，輸入改變時自動失效。
//
// Best 從每個起點往後列舉讀音；這裡改成從每個終點往前接：pre[m] 是結束在位置 m、詞庫裡還接得下去的讀音（含起點），
// 結束在 m 的詞弧由 pre[q]（以及從 q 開始的新詞）接上「從 q 出發、走到 m」的假設得到，都只看 m 之前的列。打字時音節格
// 只在尾端改變，所以只要從第一個不同的位置往後重算：每按一顆鍵的成本是「最後幾個位置的前綴 × 新的假設」，跟句子長度
// 大致無關。前向狀態與前綴保護（longerEnd）同理沿用。
//
// 結果跟同一份音節格、鎖定與解碼器設定的 Best 相同（TestSearchMatchesBest 逐位元比對）：詞弧的集合、每組（起點、終點）
// 的裁切與狀態推進的順序都一樣，只有分數完全相同的兩條讀音被列舉的先後可能不同。
//
// 解碼器設定、前文、評分源或個人資料版本改變時整個重算；有暫時證據或更正扣分時（Evidence、CorrectionPenalty 是依位置
// 的函式，比不出有沒有改變）直接用 Best。零值可以直接用。
type Search struct {
	key   searchKey
	lat   input.Lattice
	locks map[int]Lock
	pre   [][]keyPrefix // pre[m]：結束在 m 的讀音
	arcs  [][]arcSet    // arcs[m]：結束在 m 的詞弧，依起點
	words [][]int       // words[m]：有完整多音節詞（不算自動修正）結束在 m 的起點（前綴保護用）
	best  [][]state
	// capRepeat 的快取（同 Decoder 的 repeats、repeatSpans、contests）
	repeats     map[repeatKey]*repeatWords
	repeatSpans map[[3]int][]spanEntry
	contests    map[[3]int]*spanContest

	// Reused 是上一次 BestSearch 沿用了多少個位置的前向狀態（含位置 0），給測試確認真的是增量。
	Reused int
}

// maxBOSCache 是 Decoder.bosMemo 最多記幾個詞，超過就清掉重記（打一句話通常是幾千個）。
const maxBOSCache = 1 << 16

type keyPrefix struct {
	start           int
	reading         []zhuyin.Syllable
	cuts            []int
	cost            float64
	untoned, abbrev bool
	corrected       bool // 走過觸控修正的假設（input.Hyp.Corrected）
}

type arcSet struct {
	start int
	arcs  []arc
}

type searchKey struct {
	left, lex, score   any
	beam, topM, topB   int
	cont, maxCost      float64
	separate, adaptive bool
	gen                int
	time               int64
	priorOwner         any
	priorVersion       uint64
}

func (d *Decoder) searchKey(adaptive bool) (searchKey, bool) {
	k := searchKey{left: d.InitialLeft(), lex: d.Lex, score: d.Score, beam: d.Beam, topM: d.TopM, topB: d.TopB,
		cont: d.Continue, maxCost: d.MaxCost, separate: d.SeparateContinuation, adaptive: adaptive}
	if !reflect.ValueOf(d.Lex).Comparable() || !reflect.ValueOf(d.Score).Comparable() {
		return k, false
	}
	// 呼叫端每次可能包一個新的評分器（session 的前文評分器是 &documentScorer{...}）：內容相同就算相同。
	if v := reflect.ValueOf(d.Score); v.Kind() == reflect.Pointer && !v.IsNil() && v.Elem().Comparable() {
		k.score = v.Elem().Interface()
	}
	if v, ok := d.Score.(MassVersion); ok {
		k.gen, k.time = v.MassVersion()
	} else if adaptive {
		return k, false // 有學到的計數卻不報版本：不知道什麼時候改變
	}
	if v, ok := d.Score.(PriorVersion); ok {
		k.priorOwner, k.priorVersion = v.PriorVersion()
		if !reflect.ValueOf(k.priorOwner).Comparable() {
			return k, false
		}
	}
	return k, true
}

// BestSearch 同 Best，沿用 s 存的上一次結果（s 為 nil 時就是 Best）。
func (d *Decoder) BestSearch(s *Search, lat input.Lattice, locks map[int]Lock, prefix bool) ([]Seg, float64) {
	if s == nil || d.Evidence != nil || d.CorrectionPenalty != nil {
		if s != nil {
			*s = Search{}
		}
		return d.Best(lat, locks, prefix)
	}
	adaptive := d.prepareScale(lat)
	n := len(lat)
	key, ok := d.searchKey(adaptive)
	if !ok || n == 0 {
		*s = Search{}
		d.prepareMass(lat)
		if n == 0 {
			return nil, 0
		}
		return d.finish(lat, locks, prefix, d.forward(lat, locks), nil)
	}
	// p0：結束在 p0 以前（含）的假設都沒變的最後一個位置（再看鎖定）。結束在 m 的讀音與詞弧只讀結束在 m 以前（含）
	// 的假設，可以沿用。按鍵圖上新的一顆鍵讓前面幾列多了走到新位置的假設，但結束在舊位置的都不變，所以只要算新的位置。
	p0 := 0
	same := s.key == key && s.best != nil
	if same {
		p0 = min(len(s.lat), n)
		w := max(s.lat.Width(), lat.Width())
		for m := 1; m <= p0; m++ {
			if !sameEnding(s.lat, lat, m, w) {
				p0 = m - 1
				break
			}
		}
		for at, l := range locks {
			if old, ok := s.locks[at]; !ok || old != l {
				p0 = min(p0, at)
			}
		}
		for at, l := range s.locks {
			if cur, ok := locks[at]; !ok || cur != l {
				p0 = min(p0, at)
			}
		}
	}
	if d.bosKey != key || d.bosMemo == nil || len(d.bosMemo) > maxBOSCache {
		d.bosKey, d.bosMemo = key, map[string]float64{}
	}
	d.bos = d.bosMemo
	defer func() { d.bos = nil }()
	pre := make([][]keyPrefix, n+1)
	arcs := make([][]arcSet, n+1)
	words := make([][]int, n+1)
	if same {
		copy(pre, s.pre[:p0+1])
		copy(arcs, s.arcs[:p0+1])
		copy(words, s.words[:p0+1])
	}
	for m := p0 + 1; m <= n; m++ {
		pre[m], arcs[m], words[m] = d.extendTo(lat, locks, pre, m)
	}
	// 前綴保護：從各起點出發最長的完整詞結束在哪。起點 i 的最長詞從 e1 變成 e2 時，變的只有結束在
	// [min(e1, e2), max(e1, e2)) 的弧是不是「更長的詞的前綴」（arcProbability、readingLift），以及從那裡開始的弧
	// 前面那個詞是不是被拆開（leftSplit）；結束在 min(e1, e2) 之前的狀態不受影響，沿用到那裡。沒有更長的詞時
	// 等於結束在 i+1（沒有弧受保護）。之前一律退回 i，學習打開時每多一個長詞，從它的起點往後的狀態都重算。
	p := p0
	if adaptive {
		longer := make([]int, n)
		for m := 1; m <= n; m++ {
			for _, st := range words[m] {
				longer[st] = m
			}
		}
		if same {
			old := make([]int, len(s.lat))
			for m := 1; m < len(s.words); m++ {
				for _, st := range s.words[m] {
					old[st] = m
				}
			}
			for i := range min(p0, len(old)) {
				if e1, e2 := min(old[i], p0+1), min(longer[i], p0+1); e1 != e2 {
					p = min(p, max(min(e1, e2), i+1)-1)
				}
			}
		}
		d.longerEnd = longer
	}
	best := make([][]state, n+1)
	if same && p > 0 {
		copy(best, s.best[:p+1])
	} else {
		p = 0
		best[0] = []state{{last: d.InitialLeft()}}
	}
	s.Reused = p + 1
	// 疊字上限的列舉（capRepeat 的快取）只看 [at, end) 的列、鎖定與前綴保護：end 不超過 p 的沿用。
	d.repeats, d.repeatSpans, d.contests = nil, nil, nil
	if same {
		for k, v := range s.repeats {
			if k.end <= p {
				if d.repeats == nil {
					d.repeats = map[repeatKey]*repeatWords{}
				}
				d.repeats[k] = v
			}
		}
		for k, v := range s.repeatSpans {
			if k[2] <= p {
				if d.repeatSpans == nil {
					d.repeatSpans = map[[3]int][]spanEntry{}
				}
				d.repeatSpans[k] = v
			}
		}
		for k, v := range s.contests {
			if k[2] <= p {
				if d.contests == nil {
					d.contests = map[[3]int]*spanContest{}
				}
				d.contests[k] = v
			}
		}
	}
	for m := p + 1; m <= n; m++ {
		best[m] = d.relax(lat, locks, best, m, arcs[m])
	}
	// 交給 s 保管，解碼器的其他搜尋清快取時不會清到它。
	s.repeats, s.repeatSpans, s.contests = d.repeats, d.repeatSpans, d.contests
	d.repeats, d.repeatSpans, d.contests = nil, nil, nil
	s.key, s.pre, s.arcs, s.words, s.best = key, pre, arcs, words, best
	s.lat = make(input.Lattice, n)
	for i, row := range lat {
		s.lat[i] = slices.Clone(row) // 呼叫端之後可能在原本的列上 append（lockLattice），存一份
	}
	s.locks = make(map[int]Lock, len(locks))
	for k, l := range locks {
		s.locks[k] = l
	}
	return d.finish(lat, locks, prefix, best, func(i int) bool {
		// 沒有從 i 出發、走到終點還接得下去的讀音：尾端詞條一定是空的。
		return !slices.ContainsFunc(pre[n], func(x keyPrefix) bool { return x.start == i })
	})
}

// sameEnding 回報兩份音節格結束在 m 的假設（依起點、各列的順序）是否相同；w 是兩份裡最寬的假設。
func sameEnding(a, b input.Lattice, m, w int) bool {
	for q := max(0, m-w); q < m; q++ {
		i, j := 0, 0
		for {
			for i < len(a[q]) && q+a[q][i].Width() != m {
				i++
			}
			for j < len(b[q]) && q+b[q][j].Width() != m {
				j++
			}
			if i == len(a[q]) || j == len(b[q]) {
				if i != len(a[q]) || j != len(b[q]) {
					return false
				}
				break
			}
			if a[q][i] != b[q][j] {
				return false
			}
			i, j = i+1, j+1
		}
	}
	return true
}

// extendTo 算結束在 m 的讀音、詞弧與完整詞的起點：pre[q]（以及從 q 開始的新詞）接上從 q 出發、走到 m 的假設。
// 剪枝、成本上限與詞長上限同 spans；詞弧的裁切同 wordArcs（每組起點、終點各自裁，按鍵圖看讀音實際走過的假設）。
func (d *Decoder) extendTo(lat input.Lattice, locks map[int]Lock, pre [][]keyPrefix, m int) ([]keyPrefix, []arcSet, []int) {
	limit := MaxLen(d.Lex)
	var out []keyPrefix
	type pool struct {
		plain, untoned, fixed, abbrev []arc
	}
	pools := map[int]*pool{}
	var starts []int
	longer := map[int]bool{}
	for q := max(0, m-d.width); q < m; q++ {
		var row []input.Hyp
		for _, h := range lat[q] {
			if q+h.Width() == m {
				row = append(row, h)
			}
		}
		if len(row) == 0 {
			continue
		}
		try := func(src keyPrefix) {
			if len(src.reading) >= limit {
				return
			}
			var next map[zhuyin.Syllable]bool
			if len(lat[q]) > continuationRow {
				next = map[zhuyin.Syllable]bool{}
				if !lexicon.Continuations(d.Lex, src.reading, func(y zhuyin.Syllable) { next[y] = true }) {
					next = nil
				}
			}
			for _, h := range row {
				if d.MaxCost > 0 && src.cost+h.Cost > d.MaxCost {
					continue
				}
				if next != nil && !next[h.Syl] {
					continue
				}
				r := append(src.reading[:len(src.reading):len(src.reading)], h.Syl)
				if next == nil && !d.Lex.HasPrefix(r) {
					continue
				}
				c := src.cuts
				if h.Span > 1 || src.cuts != nil {
					if src.cuts == nil {
						c = make([]int, len(src.reading), len(src.reading)+1)
						for k := range c {
							c[k] = src.start + k + 1
						}
					}
					c = append(c[:len(c):len(c)], m)
				}
				x := keyPrefix{src.start, r, c, src.cost + h.Cost, src.untoned || h.Untoned, src.abbrev || h.Abbreviated, src.corrected || h.Corrected}
				out = append(out, x)
				free := !lockTouches(locks, x.start, m)
				var pooled, short bool
				if d.width > 1 {
					pooled = x.untoned && free
					short = pooled && x.abbrev
				} else {
					pooled = untonedSpan(lat, x.start, m) && free
					short = pooled && abbreviatedSpan(lat, x.start, m)
				}
				cp := ref(c)
				for _, e := range d.Lex.Exact(r) {
					if len(r) > 1 && !e.Correction {
						longer[x.start] = true
					}
					if !allowed(locks, x.start, m, e.Surface, c) {
						continue
					}
					p := pools[x.start]
					if p == nil {
						p = &pool{}
						pools[x.start] = p
						starts = append(starts, x.start)
					}
					a := arc{m, e, x.cost, cp}
					switch {
					case x.corrected && pooled:
						p.fixed = append(p.fixed, a)
					case short:
						p.abbrev = append(p.abbrev, a)
					case pooled:
						p.untoned = append(p.untoned, a)
					default:
						p.plain = append(p.plain, a)
					}
				}
			}
		}
		try(keyPrefix{start: q})
		for _, src := range pre[q] {
			try(src)
		}
	}
	slices.Sort(starts)
	sets := make([]arcSet, 0, len(starts))
	for _, st := range starts {
		p := pools[st]
		a := p.plain
		a = append(a, d.topUntonedArcs(map[int][]arc{m: p.untoned}, maxUntonedArcs, false)...)
		a = append(a, d.topUntonedArcs(map[int][]arc{m: p.fixed}, maxCorrectedArcs, true)...)
		a = append(a, d.topUntonedArcs(map[int][]arc{m: p.abbrev}, maxAbbreviatedArcs, true)...)
		sets = append(sets, arcSet{st, a})
	}
	var ws []int
	for st := range longer {
		ws = append(ws, st)
	}
	slices.Sort(ws)
	return out, sets, ws
}

// relax 是結束在 m 的前向狀態：照 forward 的順序（起點由小到大，每個起點的狀態依 frontier，弧依 wordArcs 的順序）推進。
func (d *Decoder) relax(lat input.Lattice, locks map[int]Lock, best [][]state, m int, sets []arcSet) []state {
	var out []state
	at := map[string]int{}
	for _, set := range sets {
		if len(best[set.start]) == 0 {
			continue
		}
		for _, st := range d.frontier(best[set.start]) {
			for _, a := range set.arcs {
				sc := st.score + d.repeatScore(lat, locks, st.path.last(), set.start, m, a.e.Surface, d.arcLog(set.start, m, st.last, a.e, a.cost), deref(a.cuts))
				seg := Seg{Word: a.e.Surface, Start: set.start, End: m, Correction: a.e.Correction, Matched: segMatched(lat, set.start, a.e, a.cost, deref(a.cuts)), Cuts: a.cuts}
				next := state{a.e.Surface, sc, st.path.push(seg)}
				if k, ok := at[next.last]; ok {
					if next.score > out[k].score {
						out[k] = next
					}
					continue
				}
				at[next.last] = len(out)
				out = append(out, next)
			}
		}
	}
	return out
}
