package session

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// AutoChange 說明目前顯示的一處自動近音修正，供診斷與評測；不進快照、不進學習，也不影響行為。
type AutoChange struct {
	Start, End int      // 音節位置，半開區間
	Text       string   // 這一處顯示的字
	Typed      string   // 使用者打的讀音（空白分隔）
	Matched    string   // 詞庫實際走的讀音
	Rules      []string // 用到的配對代號，每個換音音節各列一次，聲母配對在前
	Cost       float64  // 通道成本（每次換音 fuzzy.SwapCost）
	Gain       float64  // 近音路徑分數減統計首選分數；Required 時為 0
	Threshold  float64  // 當時要求的門檻；Required 時為 0
	Required   bool     // 精確讀音讀不出來而一定換音，不經過門檻
}

// String 是一行診斷文字：顯示的字、位置、打的音與實際讀音、配對、成本，以及通過門檻的依據。
func (c AutoChange) String() string {
	why := fmt.Sprintf("領先 %.1f ≥ 門檻 %.1f", c.Gain, c.Threshold)
	if c.Required {
		why = "必須換音（精確讀音讀不出來）"
	}
	return fmt.Sprintf("%s [%d,%d) 打 %s → 讀 %s；%s；成本 %.1f；%s", c.Text, c.Start, c.End, c.Typed, c.Matched, strings.Join(c.Rules, "＋"), c.Cost, why)
}

// AutoChanges 回傳目前顯示路徑中的自動近音修正；沒有時為 nil。回傳的是複本。
func (s *Session) AutoChanges() []AutoChange {
	s.refresh()
	return slices.Clone(s.autoLog)
}

// autoBlock 是原音首選與近音路徑不同的一處（相對於字面符號分段起點的音節位置，半開區間）。
type autoBlock struct{ from, to int }

// autoFuzzy 在原音路徑（含神經重排）決定後，對啟用 auto 的配對嘗試受控的自動修正：
// 近音路徑（分數已含通道成本）要比原音首選領先 AutoPolicy 的門檻，才改未提交的組字。
// 每一處不同各自比較——一處很有把握、另一處沒把握時，只改前者；改的只有那一處，
// 其餘位置保留原音路徑的字（不因重新解碼而連帶換掉不相干的同音詞）。使用者鎖定永遠優先：
// 與鎖定相交的處不改。神經重排改過的分段：近音路徑要贏過的是統計首選（神經不認得近音，沒有分數可比），
// 贏了才改該處，其餘照神經顯示的字；開發資料上這比整段跳過多修復一倍而乾淨誤改沒有增加（docs/47）。
// 精確讀音讀不出來的音節（例如 ㄙˊ，不在音節表）讓整段原音解碼落空、組字區只能顯示原樣注音；
// auto 模式下這些音節一定換成近音讀音，整段才有字可顯示。
// 不修改任何解碼器設定的最終狀態；解出的路徑只改顯示，不進學習（Seg.Matched）。
func (s *Session) autoFuzzy() {
	s.autoLog = nil
	auto := s.Fuzzy.AutoOnly()
	if !auto.Enabled() || len(s.syls) == 0 || s.english {
		return
	}
	d := s.dec
	saved := struct {
		left     string
		score    decode.Scorer
		evidence func(start, end int, left, word string) (float64, float64)
		penalty  func(start, end int, word string) float64
		usage    func(start int, word string) float64
		maxCost  float64
	}{d.Left, d.Score, d.Evidence, d.CorrectionPenalty, d.CorrectionUsage, d.MaxCost}
	defer func() {
		d.Left, d.Score, d.Evidence, d.CorrectionPenalty, d.CorrectionUsage, d.MaxCost = saved.left, saved.score, saved.evidence, saved.penalty, saved.usage, saved.maxCost
	}()
	lat := s.lattice()
	missing := len(s.path) == 0 // 原音解碼整段落空：每一段都要由這裡產生路徑
	var out []decode.Seg
	changed := false
	for start := 0; start < len(s.syls); {
		if s.literals[start] {
			out = append(out, decode.Seg{Word: string(s.syls[start]), Start: start, End: start + 1})
			start++
			continue
		}
		from, end := start, start+1
		for end < len(s.syls) && !s.literals[end] {
			end++
		}
		start = end
		var shown []decode.Seg
		if !missing {
			for _, g := range s.path {
				if g.Start >= from && g.End <= end {
					shown = append(shown, g)
				}
			}
		}
		path, ok, diff := s.autoRange(auto, lat, from, end, shown, missing)
		if !ok {
			if !missing {
				out = append(out, shown...)
				continue
			}
			return // 這一段原音與近音都解不出來：維持原樣注音保底
		}
		out = append(out, path...)
		changed = changed || diff
	}
	if changed {
		s.path = out
		s.hint = nil // 次佳提示對的是原音路徑，不再適用
	}
}

// autoRange 處理 [start, end) 一個字面符號分段；回傳這一段的路徑（絕對位置）、是否取得路徑、與原顯示是否不同。
// missing 為真時原音解碼已落空，沒有「原顯示」可比；沒有任何改動需求的段落仍回傳它的原音路徑。
func (s *Session) autoRange(auto fuzzy.Config, lat input.Lattice, start, end int, shown []decode.Seg, missing bool) ([]decode.Seg, bool, bool) {
	rel, syls := lat[start:end], s.syls[start:end]
	stable := s.completedExactReadings(start, end)
	keepReadings := func(wide input.Lattice) input.Lattice {
		for i, keep := range stable {
			if keep {
				wide[i] = rel[i]
			}
		}
		return wide
	}
	wide := keepReadings(auto.Widen(rel, syls, 0, end-start))
	if !missing && !widens(rel, wide) {
		return nil, false, false // 這段沒有可換音的音節：與沒有自動修正時完全相同，不多做任何解碼
	}
	locks := s.locksIn(start, end)
	d, prefix := s.dec, s.Opt.PrefixAware
	s.configureDocumentContext(start)
	d.Evidence = s.localEvidence(start)
	s.configureCorrections(start, end, true)
	d.MaxCost = 0
	base, s0 := d.Best(rel, locks, prefix)
	baseLat := rel
	statTop := base          // 統計首選；顯示是神經改過的路徑時，用來辨認哪些處是神經改的
	var required []autoBlock // 精確讀音讀不出來、一定要換音的位置
	switch {
	case len(base) == 0:
		required = s.unreadable(auto, syls)
		if len(required) == 0 {
			return nil, false, false
		}
		baseLat = widenBlocks(auto, rel, syls, required)
		d.MaxCost = fuzzy.MaxWordCost
		if base, s0 = d.Best(baseLat, locks, prefix); len(base) == 0 {
			return nil, false, false
		}
	case !missing && !sameWords(base, shown, start):
		// 顯示已不是統計首選（神經改選等）：近音路徑仍要贏過統計首選的分數，改的處則接在顯示的路徑上，
		// 顯示的字不在改動範圍內就不動。
		base = slices.Clone(shown)
		for i := range base {
			base[i].Start -= start
			base[i].End -= start
		}
	}
	d.MaxCost = fuzzy.MaxWordCost
	near, _ := d.Best(wide, locks, prefix)
	var chosen []chosenBlock // 自願換音、通過門檻的處（必須換音的位置不計）
	for _, b := range diffBlocks(base, near) {
		if overlapsLock(locks, b) {
			continue
		}
		path, score := d.Best(keepReadings(auto.Widen(baseLat, syls, b.from, b.to)), locks, prefix)
		swaps := voluntarySwaps(path, syls, b, required)
		if swaps == 0 || sameWords(path, base, 0) {
			continue
		}
		if need := s.Opt.FuzzyAuto.ThresholdFor(b.to-b.from, swaps, neuralTouched(base, statTop, b)); score-s0 >= need {
			chosen = append(chosen, chosenBlock{b, score - s0, need})
		}
	}
	shift := func(path []decode.Seg) []decode.Seg {
		for i := range path {
			path[i].Start += start
			path[i].End += start
		}
		return path
	}
	if len(chosen) == 0 {
		if len(required) == 0 && !missing {
			return nil, false, false
		}
		s.logAuto(start, base, syls, required, required, nil)
		return shift(base), true, true // 沒有自願的改動：原音路徑（含必須換音的位置）
	}
	picked := make([]autoBlock, len(chosen))
	for i, c := range chosen {
		picked[i] = c.autoBlock
	}
	blocks := mergeBlocks(append(slices.Clone(required), picked...))
	final, _ := d.Best(keepReadings(widenBlocks(auto, rel, syls, blocks)), locks, prefix)
	path, ok := splice(base, final, blocks)
	if !ok {
		if len(required) > 0 || missing {
			s.logAuto(start, base, syls, required, required, nil)
			return shift(base), true, true
		}
		return nil, false, false
	}
	s.logAuto(start, path, syls, blocks, required, chosen)
	return shift(path), true, true
}

// completedExactReadings 只在原讀音完整保留、尾端增加音節時保護已顯示的原音詞。
// 保護的是讀音，不鎖字面：同音重排與組成更長的詞照常，候選中的近音也仍可手選。
// 不保護單字、沒有完整詞條的長詞前綴或已修正的讀音，讓新輸入仍能得到原有的自動修正。
func (s *Session) completedExactReadings(start, end int) []bool {
	if len(s.shownSyls) == 0 || len(s.syls) <= len(s.shownSyls) ||
		!slices.Equal(s.shownSyls, s.syls[:len(s.shownSyls)]) {
		return nil
	}
	var stable []bool
	lex := s.model.Source()
	for i, first := range s.shownPath {
		if first.Start < start || first.Start >= end {
			continue
		}
		var word strings.Builder
		at := first.Start
		for _, g := range s.shownPath[i:] {
			if g.Start != at || g.End > end || g.End > len(s.shownSyls) ||
				g.End-first.Start > decode.MaxWordLen || g.Correction || g.Matched != "" ||
				utf8.RuneCountInString(g.Word) != g.End-g.Start || s.literals[g.Start] {
				break
			}
			word.WriteString(g.Word)
			at = g.End
			if at-first.Start < 2 {
				continue
			}
			for _, e := range lex.Exact(s.shownSyls[first.Start:at]) {
				if e.Correction || e.Surface != word.String() {
					continue
				}
				if stable == nil {
					stable = make([]bool, end-start)
				}
				for p := first.Start; p < at; p++ {
					stable[p-start] = true
				}
				break
			}
		}
	}
	return stable
}

// chosenBlock 是通過門檻的一處，連同當時的領先分數與要求的門檻。
type chosenBlock struct {
	autoBlock
	gain, need float64
}

// logAuto 把這一段實際套用的區塊記進 autoLog（診斷用）。path 的位置相對於分段起點 start。
func (s *Session) logAuto(start int, path []decode.Seg, syls []zhuyin.Syllable, applied, required []autoBlock, chosen []chosenBlock) {
	matched := slices.Clone(syls) // 每個位置實際走的音節
	for _, g := range path {
		if g.Matched == "" {
			continue
		}
		for k, y := range zhuyin.ParseReading(g.Matched) {
			if at := g.Start + k; at < len(matched) {
				matched[at] = y
			}
		}
	}
	for _, b := range applied {
		ch := AutoChange{Start: start + b.from, End: start + b.to,
			Typed:   strings.TrimSpace(zhuyin.Key(syls[b.from:b.to])),
			Matched: strings.TrimSpace(zhuyin.Key(matched[b.from:b.to])), Required: true}
		for _, g := range path {
			if g.Start >= b.from && g.End <= b.to {
				ch.Text += g.Word
			}
		}
		for i := b.from; i < b.to; i++ {
			for _, r := range fuzzy.Rules(syls[i], matched[i]) {
				ch.Rules = append(ch.Rules, r.ID())
				ch.Cost += fuzzy.SwapCost
			}
		}
		for _, c := range chosen {
			if c.from < b.to && b.from < c.to && (ch.Required || c.gain > ch.Gain) {
				ch.Required, ch.Gain, ch.Threshold = false, c.gain, c.need
			}
		}
		s.autoLog = append(s.autoLog, ch)
	}
}

// neuralTouched 回報顯示路徑 shown 在區塊 b 內是否與統計首選不同（神經改過）。
func neuralTouched(shown, statTop []decode.Seg, b autoBlock) bool {
	if len(statTop) == 0 {
		return false // 沒有統計首選可比（精確讀音讀不出來）：不是神經改的
	}
	for _, g := range shown {
		if g.Start < b.to && b.from < g.End && !slices.ContainsFunc(statTop, func(t decode.Seg) bool {
			return t.Word == g.Word && t.Start == g.Start && t.End == g.End
		}) {
			return true
		}
	}
	return false
}

// unreadable 回傳精確讀音沒有任何單音節詞條、且在 auto 配對下有近音替代的音節位置（合併相鄰者）。
func (s *Session) unreadable(auto fuzzy.Config, syls []zhuyin.Syllable) []autoBlock {
	var out []autoBlock
	for i, y := range syls {
		if len(s.dec.Lex.Exact([]zhuyin.Syllable{y})) > 0 || len(auto.Alternatives(y)) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].to == i {
			out[n-1].to = i + 1
		} else {
			out = append(out, autoBlock{i, i + 1})
		}
	}
	return out
}

// widenBlocks 只在 blocks 內補上近音假設。
func widenBlocks(auto fuzzy.Config, rel input.Lattice, syls []zhuyin.Syllable, blocks []autoBlock) input.Lattice {
	out := slices.Clone(rel)
	for _, b := range blocks {
		w := auto.Widen(rel, syls, b.from, b.to)
		for i := b.from; i < b.to; i++ {
			out[i] = w[i]
		}
	}
	return out
}

// mergeBlocks 把重疊或相鄰的區塊併成一個，依起點排序。
func mergeBlocks(blocks []autoBlock) []autoBlock {
	sorted := slices.Clone(blocks)
	slices.SortFunc(sorted, func(a, b autoBlock) int { return a.from - b.from })
	var out []autoBlock
	for _, b := range sorted {
		if n := len(out); n > 0 && b.from <= out[n-1].to {
			out[n-1].to = max(out[n-1].to, b.to)
		} else {
			out = append(out, b)
		}
	}
	return out
}

// voluntarySwaps 是路徑在區塊 b 內換音的音節數，扣掉本來就必須換音（required）的位置。
func voluntarySwaps(path []decode.Seg, syls []zhuyin.Syllable, b autoBlock, required []autoBlock) int {
	n := 0
	for _, at := range swappedPositions(path, syls) {
		if at >= b.from && at < b.to && !slices.ContainsFunc(required, func(r autoBlock) bool { return at >= r.from && at < r.to }) {
			n++
		}
	}
	return n
}

// splice 以 final 路徑中落在 blocks 內的詞取代 base 路徑對應的詞；blocks 外保留 base。
// final 在每個區塊邊界都要有詞界、區塊內的詞要剛好鋪滿區塊，否則回傳 false。
func splice(base, final []decode.Seg, blocks []autoBlock) ([]decode.Seg, bool) {
	sorted := slices.Clone(blocks)
	slices.SortFunc(sorted, func(a, b autoBlock) int { return a.from - b.from })
	var out []decode.Seg
	i := 0
	for _, b := range sorted {
		for i < len(base) && base[i].End <= b.from {
			out = append(out, base[i])
			i++
		}
		if i < len(base) && base[i].Start < b.from {
			return nil, false // base 的詞跨過區塊起點
		}
		for i < len(base) && base[i].Start < b.to {
			if base[i].End > b.to {
				return nil, false
			}
			i++
		}
		at := b.from
		for _, g := range final {
			if g.Start >= b.from && g.End <= b.to {
				if g.Start != at {
					return nil, false
				}
				out = append(out, g)
				at = g.End
			} else if g.Start < b.to && g.End > b.from {
				return nil, false // final 的詞跨過區塊邊界
			}
		}
		if at != b.to {
			return nil, false
		}
	}
	return append(out, base[i:]...), true
}

// widens 回報 wide 是否比 narrow 多了任何音節假設。
func widens(narrow, wide input.Lattice) bool {
	for i := range narrow {
		if len(wide[i]) > len(narrow[i]) {
			return true
		}
	}
	return false
}

// sameWords 回報兩條路徑（b 的位置加上 shift 後）的詞與邊界是否相同。
func sameWords(a, b []decode.Seg, shift int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Word != b[i].Word || a[i].Start+shift != b[i].Start || a[i].End+shift != b[i].End {
			return false
		}
	}
	return true
}

// diffBlocks 回傳近音路徑與原音路徑不同、且近音一側含有換音詞的處；兩條路徑在同一位置同時有詞界、
// 且之後的詞相同時，一處結束。
func diffBlocks(exact, near []decode.Seg) []autoBlock {
	var out []autoBlock
	i, j := 0, 0
	for i < len(near) && j < len(exact) {
		if near[i].Word == exact[j].Word && near[i].Start == exact[j].Start && near[i].End == exact[j].End {
			i++
			j++
			continue
		}
		from, swapped := near[i].Start, false
		for {
			swapped = swapped || near[i].Matched != ""
			ne, ee := near[i].End, exact[j].End
			switch {
			case ne < ee:
				i++
			case ee < ne:
				j++
			default:
				i++
				j++
			}
			if ne == ee || i >= len(near) || j >= len(exact) {
				if swapped {
					out = append(out, autoBlock{from, max(ne, ee)})
				}
				break
			}
		}
	}
	return out
}

func overlapsLock(locks map[int]decode.Lock, b autoBlock) bool {
	for at, l := range locks {
		if at < b.to && b.from < at+l.Len {
			return true
		}
	}
	return false
}

// swappedPositions 列出路徑中讀音與打出的音不同的音節位置（相對於分段起點）。
func swappedPositions(path []decode.Seg, syls []zhuyin.Syllable) []int {
	var out []int
	for _, g := range path {
		if g.Matched == "" {
			continue
		}
		for k, y := range zhuyin.ParseReading(g.Matched) {
			if at := g.Start + k; at < len(syls) && y != syls[at] {
				out = append(out, at)
			}
		}
	}
	return out
}
