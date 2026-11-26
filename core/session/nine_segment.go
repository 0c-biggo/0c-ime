package session

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 注音九宮格自動切字（Session.NineSegment，手機，預設關）：使用者連續按九宮格的鍵、不必按聲調鍵，
// 核心在鍵上同時決定切法與選字。
//
// 組字的單位是「完整音節或九宮格鍵」：九宮格音節記著自己的鍵（nineSeg，隨插入、刪除平移），還沒固定的連續幾個
// 九宮格音節是一段可以重新切的鍵（run）。每次請求處理完（Handle 的最後，resegment）把整段組字換成按鍵圖：
// run 裡的位置是鍵，每 1–3 顆鍵組得出合法音節就是一條音節弧（input.Hyp.Span），其他位置照原本的音節格；
// decode 在圖上一次決定切法與選字（逐鍵增量的 decode.Search），解出的切法寫回組字：run 換成新切出來的音節。
// 之後的顯示、候選、學習、神經評分都照「目前的切法」在一般的音節格上做，跟關閉時一樣，協定的位置也都是音節。
//
//   - 聲調鍵是錨點：音節在那裡結束，只有一個聲調時那個音節帶那個聲調。錨點之前的鍵照樣可以改切法，切點不跨過它。
//   - 游標在 run 中間時，游標也是切點，打的鍵接在游標處，不會跟後面的鍵合成一個音節。
//   - 固定（fixed）：使用者用左欄（nine_reading）、選字或 fix 定下的音節，切點與拼法不再改，鍵仍記著（退格一次刪一顆）。
//     鎖定蓋到的九宮格音節一律固定（fixLockedNine），鎖定的邊界因此永遠是音節邊界。
//   - 尾端：run 最後一串還沒按聲調鍵的鍵可以是還沒打完的音節，展開成以它開頭的音節（同簡拼，標 Abbreviated）。
//
// 關閉時這裡的程式都不會走到：nineSeg 是空的，簽章、音節格與快照都跟沒有這個功能時相同。

// nineSyl 是自動切字的一個九宮格音節。
type nineSyl struct {
	keys   []string        // 每顆鍵的那組注音
	anchor int             // 這個音節後面按了聲調鍵：-1 沒按、0 聲調未指定、1–5 聲調
	fixed  zhuyin.Syllable // 固定的拼法（不帶聲調）；空字串是還沒固定，切點與拼法由解碼決定
}

func (n nineSyl) tone() int { return max(n.anchor, 0) }

func (n nineSyl) equal(o nineSyl) bool {
	return n.anchor == o.anchor && n.fixed == o.fixed && slices.Equal(n.keys, o.keys)
}

// inRun 回報第 at 個音節是不是還沒固定的九宮格音節（可以重新切）。
func (s *Session) inRun(at int) bool {
	n, ok := s.nineSeg[at]
	return ok && n.fixed == ""
}

// nineSegOpen 回報游標前是不是還沒固定、沒按聲調鍵的九宮格音節：新的鍵接在它後面，聲調鍵落在它上面。
func (s *Session) nineSegOpen() bool {
	if !s.NineSegment || s.cursor == 0 {
		return false
	}
	n, ok := s.nineSeg[s.cursor-1]
	return ok && n.fixed == "" && n.anchor < 0
}

// nineSegSymbol 是自動切字時的 symbol 請求（同 nineSymbol 的分類）：鍵一律接上（接不上目前音節開頭的鍵由解碼切成
// 新的音節），聲調鍵是錨點。
func (s *Session) nineSegSymbol(symbol, group string) bool {
	if group == "" {
		handled := false
		for _, r := range nineGroup(symbol) {
			if zhuyin.ClassOf(r) == zhuyin.Tone {
				handled = s.nineSegAnchor(toneNumber(r)) || handled
			} else {
				handled = s.nineSegKey(string(r)) || handled
			}
		}
		return handled
	}
	keys := []rune(nineGroup(group))
	tones := 0
	for _, r := range keys {
		if zhuyin.ClassOf(r) == zhuyin.Tone {
			tones++
		}
	}
	if len(keys) == 0 || tones > 0 && tones < len(keys) {
		return false
	}
	s.adoptComposerSeg()
	if tones == 0 {
		return s.nineSegKey(string(keys))
	}
	tone := nineToneKey
	if tones == 1 {
		tone = toneNumber(keys[0])
	}
	return s.nineSegAnchor(tone)
}

// adoptComposerSeg：組音器裡有一般注音鍵打的注音時，每個注音當成只有它的一顆鍵接進九宮格（同 adoptComposer）。
func (s *Session) adoptComposerSeg() {
	if s.comp.Empty() {
		return
	}
	typed := s.comp.String()
	s.comp.Reset()
	s.compNear = nil
	for _, r := range typed {
		s.nineSegKey(string(r))
	}
}

// nineSegKey 接上一顆鍵：接在游標前那個還沒固定、沒按聲調鍵的九宮格音節後面；沒有就在游標處開一個新的音節。
// 切法等這次請求處理完再由 resegment 重新決定。開著的候選關掉（範圍跟著鍵變了），外殼照常再要一次。
func (s *Session) nineSegKey(keys string) bool {
	if keys == "" {
		return false
	}
	s.cand = nil
	if s.nineSegOpen() {
		at := s.cursor - 1
		n := s.nineSeg[at]
		n.keys = append(slices.Clip(n.keys), keys)
		s.putNineSyl(at, n)
		return true
	}
	s.insertNineSyl(s.cursor, nineSyl{keys: []string{keys}, anchor: -1})
	s.cursor++
	return true
}

// nineSegAnchor 在游標前的九宮格音節後面按聲調鍵（tone 0 是聲調未指定）。從上一個切點到這裡的鍵要切得成完整的音節，
// 否則不收（同關閉時組不成音節不收聲調鍵），鍵留著讓使用者退格。
func (s *Session) nineSegAnchor(tone int) bool {
	if !s.nineSegOpen() {
		return false
	}
	at := s.cursor - 1
	var keys []string
	for p := at; p >= 0 && s.inRun(p) && (p == at || s.nineSeg[p].anchor < 0); p-- {
		keys = append(slices.Clone(s.nineSeg[p].keys), keys...)
	}
	if !s.nineSegmentable(keys, tone) {
		return false
	}
	n := s.nineSeg[at]
	n.anchor = tone
	s.cand = nil
	s.putNineSyl(at, n)
	return true
}

// nineSegmentable 回報 keys 能不能整串切成完整的音節（最後一個音節帶聲調 tone）。
func (s *Session) nineSegmentable(keys []string, tone int) bool {
	ok := make([]bool, len(keys)+1)
	ok[0] = true
	for i := 1; i <= len(keys); i++ {
		for w := 1; w <= 3 && w <= i && !ok[i]; w++ {
			t := nineToneKey
			if i == len(keys) {
				t = tone
			}
			ok[i] = ok[i-w] && len(s.nineReadings(keys[i-w:i], t)) > 0
		}
	}
	return ok[len(keys)]
}

// nineSegBackspace：游標前是九宮格音節時退一顆鍵（後面按了聲調鍵就先退聲調鍵）。改過的音節不再固定，蓋到它的鎖定解除；
// 鍵退光了音節就拿掉。不是九宮格音節時回傳 false，照一般的退格。
func (s *Session) nineSegBackspace() bool {
	if !s.NineSegment || s.cursor == 0 {
		return false
	}
	at := s.cursor - 1
	n, ok := s.nineSeg[at]
	if !ok {
		return false
	}
	if n.anchor >= 0 {
		n.anchor = -1
	} else {
		n.keys = slices.Clip(n.keys[:len(n.keys)-1])
	}
	n.fixed = ""
	s.cand = nil
	s.dropLocksAt(at, at+1)
	if len(n.keys) == 0 {
		s.eraseBeforeCursor()
		s.syncNine()
		return true
	}
	s.putNineSyl(at, n)
	return true
}

// nineSegClear 是 Esc：清掉還沒固定的九宮格音節（同關閉時清掉還沒完成音節的鍵）；沒有時回傳 false。
func (s *Session) nineSegClear() bool {
	if !s.NineSegment {
		return false
	}
	cleared := false
	for at := len(s.syls) - 1; at >= 0; at-- {
		if s.inRun(at) {
			s.eraseAt(at)
			cleared = true
		}
	}
	if cleared {
		s.cand = nil
		s.syncNine()
	}
	return cleared
}

// dropLocksAt 解除與 [start, end) 相交的鎖定。
func (s *Session) dropLocksAt(start, end int) {
	locks := map[int]decode.Lock{}
	for at, l := range s.locks {
		if at+l.Len <= start || end <= at {
			locks[at] = l
		}
	}
	s.locks = locks
}

// putNineSyl 換掉第 at 個九宮格音節的鍵（不就地改 map：存起來準備還原的那份不跟著改）。
func (s *Session) putNineSyl(at int, n nineSyl) {
	s.nineSeg = maps.Clone(s.nineSeg)
	if s.nineSeg == nil {
		s.nineSeg = map[int]nineSyl{}
	}
	s.nineSeg[at] = n
	s.syncNine()
}

// insertNineSyl 在第 at 個音節的位置插入一個九宮格音節（不移游標；鎖定等依 shiftLocks 平移）。
func (s *Session) insertNineSyl(at int, n nineSyl) {
	s.syls = append(s.syls[:at:at], append([]zhuyin.Syllable{""}, s.syls[at:]...)...)
	s.shiftLocks(at, +1)
	if at < s.cursor {
		s.cursor++
	}
	s.putNineSyl(at, n)
}

// eraseAt 拿掉第 at 個音節（游標在它後面時跟著左移）。
func (s *Session) eraseAt(at int) {
	s.syls = append(s.syls[:at:at], s.syls[at+1:]...)
	s.shiftLocks(at, -1)
	if at < s.cursor {
		s.cursor--
	}
}

// nineRows 是一串鍵當成一個音節時的讀音：完整的拼法（依常用程度，最多 maxNineReadings 個），tail 時再加上還沒打完
// 的拼法展開的音節（同簡拼，abbrev 標 true）。聲調 tone 0 是聲調未指定。
type nineRows struct {
	readings []zhuyin.Syllable
	abbrev   []bool
	hyps     map[int][]input.Hyp // 依 Span 的音節格列（0 是一般音節格）
}

func (s *Session) nineSpan(keys []string, tone int, tail bool) *nineRows {
	key := strings.Join(keys, "|") + "|" + strconv.Itoa(tone) + "|" + strconv.FormatBool(tail)
	if r, ok := s.nineRowCache[key]; ok {
		return r
	}
	r := &nineRows{hyps: map[int][]input.Hyp{}}
	for _, y := range s.nineReadings(keys, tone) {
		r.readings, r.abbrev = append(r.readings, y), append(r.abbrev, false)
	}
	if tail {
		seen := map[zhuyin.Syllable]bool{}
		var bases []zhuyin.Syllable
		for _, prefix := range nineSpellings(keys, false) {
			if prefix.Legal() {
				continue // 完整的音節已經在上面
			}
			for _, b := range prefixBases(prefix) {
				if !seen[b] {
					seen[b] = true
					bases = append(bases, b)
				}
			}
		}
		slices.Sort(bases) // nineBases 來自 map，先排好，同分時的順序才固定
		for _, b := range byPrior(bases, s.untonedPrior) {
			r.readings, r.abbrev = append(r.readings, zhuyin.Untone(b)), append(r.abbrev, true)
		}
	}
	if s.nineRowCache == nil || len(s.nineRowCache) > 4096 {
		s.nineRowCache = map[string]*nineRows{}
	}
	s.nineRowCache[key] = r
	return r
}

// row 是這些讀音的音節格列，每個假設佔 span 個位置（0：一般的音節格）。都是打的音：成本 0、標 Untoned。
func (r *nineRows) row(span int) []input.Hyp {
	if row, ok := r.hyps[span]; ok {
		return row
	}
	var row []input.Hyp
	for i, y := range r.readings {
		for _, v := range y.Tones() {
			row = append(row, input.Hyp{Syl: v, Untoned: true, Abbreviated: r.abbrev[i], Span: span})
		}
	}
	row = slices.Clip(row) // 呼叫端在列上 append 時另配一份，不改到快取
	r.hyps[span] = row
	return row
}

// spanRows 是一串鍵在按鍵圖上當成一個音節的讀音：tail 時含還沒打完的拼法展開（nineSpan）；一顆自己組不成音節的鍵
// （長按選的單一聲母 ㄈ）不論在哪裡都展開成以它開頭的音節，同 nineSegRows 的退路，所以顯示、候選、fix 與重新切看到
// 同一張圖（游標移開之後也解得出來）。分組鍵每顆都有能自己成音節的韻母，一般的打法不受影響。
func (s *Session) spanRows(keys []string, tone int, tail bool) *nineRows {
	r := s.nineSpan(keys, tone, tail)
	if len(r.readings) == 0 && len(keys) == 1 {
		r = s.nineSpan(keys, 0, true)
	}
	return r
}

// prefixBases 是以 prefix 開頭的音節（不帶聲調），每個開頭只找一次。
func prefixBases(prefix zhuyin.Syllable) []zhuyin.Syllable {
	if v, ok := prefixBaseCache.Load(prefix); ok {
		return v.([]zhuyin.Syllable)
	}
	var out []zhuyin.Syllable
	for _, b := range nineBases {
		if strings.HasPrefix(string(b), string(prefix)) {
			out = append(out, b)
		}
	}
	prefixBaseCache.Store(prefix, out)
	return out
}

var prefixBaseCache sync.Map

// untonedPrior 是 basePrior(b, 0)（五個聲調裡最常用的），每個音節只算一次。
func (s *Session) untonedPrior(b zhuyin.Syllable) float64 {
	if p, ok := s.basePriors[b]; ok {
		return p
	}
	if s.basePriors == nil {
		s.basePriors = map[zhuyin.Syllable]float64{}
	}
	p := s.basePrior(b, 0)
	s.basePriors[b] = p
	return p
}

// startPrior 是 prefixPrior（以 p 開頭的音節裡最常用的那個），每個開頭只算一次。
func (s *Session) startPrior(p zhuyin.Syllable) float64 {
	if v, ok := s.prefixPriors[p]; ok {
		return v
	}
	if s.prefixPriors == nil {
		s.prefixPriors = map[zhuyin.Syllable]float64{}
	}
	best := 0.0
	for _, b := range prefixBases(p) {
		best = max(best, s.untonedPrior(b))
	}
	s.prefixPriors[p] = best
	return best
}

// nineSegTail 回報第 at 個九宮格音節是不是 run 的最後一個、還沒按聲調鍵：它的鍵可以是還沒打完的音節。
func (s *Session) nineSegTail(at int) bool {
	n := s.nineSeg[at]
	return n.fixed == "" && n.anchor < 0 && !s.inRun(at+1)
}

// nineSegRows 是第 at 個九宮格音節的讀音：固定的是那個拼法，否則是它的鍵當成一個音節的讀音。
func (s *Session) nineSegRows(at int) *nineRows {
	n := s.nineSeg[at]
	if n.fixed != "" {
		y := zhuyin.Untone(n.fixed)
		if n.anchor > 0 {
			y = n.fixed.WithTone(n.anchor)
		}
		return &nineRows{readings: []zhuyin.Syllable{y}, abbrev: []bool{false}, hyps: map[int][]input.Hyp{}}
	}
	r := s.nineSpan(n.keys, n.tone(), s.nineSegTail(at))
	if len(r.readings) == 0 {
		r = s.nineSpan(n.keys, n.tone(), true) // 組不成任何音節（例如切到一半）：至少列出以它開頭的音節，畫面才有東西
	}
	return r
}

// syncNine 依 nineSeg 重算每個九宮格音節的讀音（nine，音節格與 absorbNine 用）與組字的代表讀音（syls：目前的讀音
// 還在就留著，否則換成第一個）。
func (s *Session) syncNine() {
	var nine map[int][]zhuyin.Syllable
	var syls []zhuyin.Syllable
	for at := range s.nineSeg {
		if at >= len(s.syls) {
			continue
		}
		readings := s.nineSegRows(at).readings
		if !slices.Equal(s.nine[at], readings) {
			if nine == nil {
				nine = maps.Clone(s.nine)
				if nine == nil {
					nine = map[int][]zhuyin.Syllable{}
				}
			}
			nine[at] = readings
		}
		if len(readings) > 0 && !slices.Contains(readings, s.syls[at]) {
			if syls == nil {
				syls = slices.Clone(s.syls)
			}
			syls[at] = readings[0]
		}
	}
	for at := range s.nine {
		if _, ok := s.nineSeg[at]; !ok && s.NineSegment {
			if nine == nil {
				nine = maps.Clone(s.nine)
			}
			delete(nine, at) // 自動切字時 nine 只跟著 nineSeg
		}
	}
	if nine != nil {
		s.nine = nine
	}
	if syls != nil {
		s.syls = syls
	}
}

// nineSegPartial 回報第 at 個九宮格音節目前是不是還沒打完（讀音是以它的鍵開頭的音節，不是它的鍵的拼法）。
func (s *Session) nineSegPartial(at int) bool {
	n, ok := s.nineSeg[at]
	if !ok || n.fixed != "" {
		return false
	}
	return !slices.Contains(nineSpellings(n.keys, true), s.syls[at].Base())
}

// keyCoord 是第 p 個音節之前的「鍵座標」：九宮格音節算它的鍵數，其他位置各算一格。重新切、固定都不改變它，
// 游標靠它換回切過之後的位置。
func (s *Session) keyCoord(p int) int {
	c := 0
	for q := range min(p, len(s.syls)) {
		c += s.keyWidth(q)
	}
	return c
}

// keyWidth 是第 p 個音節在鍵座標上佔幾格：九宮格音節是它的鍵數，其他位置一格。
func (s *Session) keyWidth(p int) int {
	if n, ok := s.nineSeg[p]; ok {
		return len(n.keys)
	}
	return 1
}

// atKeyCoord 是鍵座標 c 所在的音節邊界；c 在一個音節的鍵中間時取那個音節的結尾。
func (s *Session) atKeyCoord(c int) int {
	at := 0
	for p := range s.syls {
		if at >= c {
			return p
		}
		at += s.keyWidth(p)
	}
	return len(s.syls)
}

// nineView 是一段組字（字面文字之間）的按鍵圖：還沒固定的九宮格音節換成它們的鍵，其他位置照原本的音節格。
type nineView struct {
	lat   input.Lattice
	unit  []int // unit[p] 是第 begin+p 個音節的第一個位置；unit[end-begin] 是總位置數
	begin int
}

// nineGraph 建 [begin, end) 重新切字用的按鍵圖。sylLat 是整段組字的音節格；cut 是 run 裡必須是切點的鍵座標（游標），
// -1 沒有。最後的鍵不展開成還沒打完的音節（切法只看完整的音節，見 resegment）。
func (s *Session) nineGraph(sylLat input.Lattice, begin, end, cut int) nineView {
	return s.keyGraph(sylLat, begin, end, cut, begin, end, false)
}

// nineGraphIn 建候選、選字與 fix 用的按鍵圖：只把 [from, to) 裡還沒固定的九宮格音節換成鍵，其他音節照目前的切法
// （音節格）；最後的鍵同顯示，展開成以它開頭的音節。候選只要候選範圍的鍵有不同切法；左右兩邊當前後文，用畫面上的
// 切法就好，而且一個音節一個位置，算得快（整張按鍵圖的後綴評分要對每一種切法的每一個前詞算一次，手機上一次列表要
// 好幾秒）。
func (s *Session) nineGraphIn(sylLat input.Lattice, begin, end, cut, from, to int) nineView {
	return s.keyGraph(sylLat, begin, end, cut, from, to, true)
}

// keyGraph 是 nineGraph、nineGraphIn 的按鍵圖；tails 時 run 最後、沒按聲調鍵的鍵展開成還沒打完的音節。
func (s *Session) keyGraph(sylLat input.Lattice, begin, end, cut, from, to int, tails bool) nineView {
	expand := func(p int) bool { return s.inRun(p) && from <= p && p < to }
	v := nineView{unit: make([]int, end-begin+1), begin: begin}
	for p := begin; p < end; p++ {
		w := 1
		if expand(p) {
			w = len(s.nineSeg[p].keys)
		}
		v.unit[p-begin+1] = v.unit[p-begin] + w
	}
	v.lat = make(input.Lattice, v.unit[end-begin])
	for p := begin; p < end; {
		if !expand(p) {
			v.lat[v.unit[p-begin]] = sylLat[p]
			p++
			continue
		}
		q := p
		for q < end && expand(q) {
			q++
		}
		runEnd := !s.inRun(q) // 換成鍵的這段後面不是還沒固定的鍵：最後的鍵才可能是還沒打完的音節
		// 切點與只能是一個音節的地方（wholeSpans）照整個 run 算：候選範圍的兩端不是切點，否則範圍的開頭到第一個錨點
		// 會被當成「兩個切點之間」，範圍裡的鍵只能合成一個音節，畫面上的切法反而放不進。
		rp, rq := p, q
		for rp > begin && s.inRun(rp-1) {
			rp--
		}
		for rq < end && s.inRun(rq) {
			rq++
		}
		all, allStops := s.runStops(rp, rq, cut)
		off := s.keyCoord(p) - s.keyCoord(rp)
		keys := all[off : off+v.unit[q-begin]-v.unit[p-begin]]
		stops := map[int]int{}
		for c, t := range allStops {
			if c > off && c <= off+len(keys) {
				stops[c-off] = t
			}
		}
		u0 := v.unit[p-begin]
		whole := map[int]int{} // 起點鍵 → 只能是那一整段的鍵數
		for _, w := range s.wholeSpans(all, allStops) {
			if w[0] < off || w[1] > off+len(keys) {
				continue // 不完全在範圍裡（範圍的兩端都是現在的音節邊界，切法照它的時候不會發生）
			}
			for k := w[0]; k < w[1]; k++ {
				whole[k-off] = w[1] - w[0]
			}
		}
		for k := range keys {
			for w := 1; w <= 3 && k+w <= len(keys); w++ {
				if n, ok := whole[k]; ok && w != n {
					continue
				}
				blocked := false
				for c := k + 1; c < k+w; c++ {
					if _, ok := stops[c]; ok {
						blocked = true
					}
				}
				if blocked {
					break // 音節不跨過錨點與游標
				}
				tone, anchored := stops[k+w]
				tail := tails && k+w == len(keys) && !anchored && runEnd
				r := s.spanRows(keys[k:k+w], max(tone, 0), tail)
				v.lat[u0+k] = append(v.lat[u0+k], r.row(w)...)
			}
		}
		p = q
	}
	return v
}

// wholeSpans 是一段 run 裡只能是一個音節的地方（鍵座標 [from, to)）：兩個切點之間的鍵不超過 3 顆、後面按了聲調鍵、
// 自己就組得成一個音節時，那就是一個音節。每個音節都按聲調鍵的人（關閉時的打法）不會被切成兩個單一注音的音節
// （ㄕㄢ 切成 ㄕ＋ㄓ）。stops 是切點（鍵座標 → 錨點的聲調；-1 是游標）。
func (s *Session) wholeSpans(keys []string, stops map[int]int) [][2]int {
	var out [][2]int
	from := 0
	for c := 1; c <= len(keys); c++ {
		t, ok := stops[c]
		if !ok && c < len(keys) {
			continue
		}
		if ok && t >= 0 && c-from <= 3 && len(s.nineSpan(keys[from:c], t, false).readings) > 0 {
			out = append(out, [2]int{from, c})
		}
		from = c
	}
	return out
}

// runStops 是 [p, q) 這段 run 的鍵與切點（錨點，以及落在中間的游標 cursor，鍵座標）。
func (s *Session) runStops(p, q, cursor int) ([]string, map[int]int) {
	var keys []string
	stops := map[int]int{}
	for r := p; r < q; r++ {
		n := s.nineSeg[r]
		keys = append(keys, n.keys...)
		if n.anchor >= 0 {
			stops[len(keys)] = n.tone()
		}
	}
	if c := cursor - s.keyCoord(p); c > 0 && c < len(keys) {
		if _, ok := stops[c]; !ok {
			stops[c] = -1
		}
	}
	return keys, stops
}

// runSyllables 是 [a, b) 這段 run 照切法 cuts（每個音節在 run 內結束的鍵座標）切出來的音節；錨點跟著鍵。
func (s *Session) runSyllables(a, b int, cuts []int) []nineSyl {
	var keys []string
	anchors := map[int]int{}
	for p := a; p < b; p++ {
		n := s.nineSeg[p]
		keys = append(keys, n.keys...)
		if n.anchor >= 0 {
			anchors[len(keys)] = n.anchor
		}
	}
	var out []nineSyl
	prev := 0
	for _, c := range cuts {
		n := nineSyl{keys: slices.Clone(keys[prev:c]), anchor: -1}
		if t, ok := anchors[c]; ok {
			n.anchor = t
		}
		out = append(out, n)
		prev = c
	}
	return out
}

// greedyCuts 是一個組不成音節的音節（解不出路徑時）的切法：從左邊起每次取最長（最多 3 顆）組得出完整音節的鍵，
// 組不出就取一顆。
func (s *Session) greedyCuts(keys []string) []int {
	var cuts []int
	for k := 0; k < len(keys); {
		take := 1
		for w := min(3, len(keys)-k); w >= 1; w-- {
			if len(s.nineReadings(keys[k:k+w], nineToneKey)) > 0 {
				take = w
				break
			}
		}
		k += take
		cuts = append(cuts, k)
	}
	return cuts
}

// replaceRun 把 [a, b) 換成 syls 這些九宮格音節（游標另外由呼叫端照鍵座標放回）。前後相同的音節不動。
func (s *Session) replaceRun(a, b int, syls []nineSyl) {
	for a < b && len(syls) > 0 && s.nineSeg[a].equal(syls[0]) {
		a, syls = a+1, syls[1:]
	}
	for a < b && len(syls) > 0 && s.nineSeg[b-1].equal(syls[len(syls)-1]) {
		b, syls = b-1, syls[:len(syls)-1]
	}
	cursor := s.cursor
	for p := b - 1; p >= a; p-- {
		s.eraseAt(p)
	}
	for i, n := range syls {
		s.syls = append(s.syls[:a+i:a+i], append([]zhuyin.Syllable{""}, s.syls[a+i:]...)...)
		s.shiftLocks(a+i, +1)
		s.nineSeg = maps.Clone(s.nineSeg)
		if s.nineSeg == nil {
			s.nineSeg = map[int]nineSyl{}
		}
		s.nineSeg[a+i] = n
	}
	s.cursor = cursor
}

// fixLockedNine 固定被鎖定蓋到的九宮格音節：拼法取鎖定的詞在那裡的讀音（找不到時用目前的讀音）。
func (s *Session) fixLockedNine(sylLat input.Lattice) {
	for at, l := range s.locks {
		touched := false
		for p := at; p < at+l.Len && p < len(s.syls); p++ {
			touched = touched || s.inRun(p)
		}
		if !touched || at+l.Len > len(s.syls) {
			continue
		}
		reading, ok := decode.Reading(s.dec.Lex, sylLat, at, at+l.Len, l.Word)
		for p := at; p < at+l.Len; p++ {
			if !s.inRun(p) {
				continue
			}
			n := s.nineSeg[p]
			n.fixed = s.syls[p].Base()
			if ok && len(reading) == l.Len {
				n.fixed = reading[p-at].Base()
			}
			s.nineSeg = maps.Clone(s.nineSeg)
			s.nineSeg[p] = n
		}
	}
}

// resegment 重新決定還沒固定的九宮格鍵的切法（見檔頭）。候選開著時不切，候選的位置才不會跑掉。
func (s *Session) resegment() {
	if !s.NineSegment || len(s.nineSeg) == 0 || s.cand != nil {
		return
	}
	// 只移動游標（APP 列句首候選時也是在複本上把游標移到句首）不重新切：游標切點只在打鍵的那一刻有意義，
	// 移開之後照樣切，畫面上的字會跟著游標變（剛在中間打的鍵移開游標就跟後面的鍵合成一個音節）。
	if sig := s.cutSignature(); sig == s.nineCutSig {
		return
	}
	defer func() { s.nineCutSig = s.cutSignature() }()
	s.syncNine()
	sylLat := s.lattice()
	s.fixLockedNine(sylLat)
	s.syncNine()
	sylLat = s.lattice()
	cursor := s.keyCoord(s.cursor)
	type runCut struct {
		a, b int
		cuts []int
	}
	var runs []runCut
	searches := map[int]*decode.Search{} // 這次用到的段（沒用到的丟掉），見 searchCopy
	s.dec.Beam, s.dec.Continue = s.Opt.Beam, s.Opt.Continue
	s.dec.SeparateContinuation = s.Opt.SeparateContinuation
	s.dec.PrefixCacheKeys = s.Opt.PrefixCacheKeys
	evidence, penalty := s.dec.Evidence, s.dec.CorrectionPenalty
	defer func() { s.dec.Evidence, s.dec.CorrectionPenalty = evidence, penalty }()
	for begin := 0; begin < len(s.syls); {
		if s.literals[begin] {
			begin++
			continue
		}
		end := begin + 1
		for end < len(s.syls) && !s.literals[end] {
			end++
		}
		hasRun := false
		for p := begin; p < end; p++ {
			hasRun = hasRun || s.inRun(p)
		}
		if !hasRun {
			begin = end
			continue
		}
		v := s.nineGraph(sylLat, begin, end, cursor)
		locks := map[int]decode.Lock{}
		for at, l := range s.locksIn(begin, end) {
			l.Len = v.unit[at+l.Len] - v.unit[at]
			locks[v.unit[at]] = l
		}
		s.configureDocumentContext(begin)
		s.dec.Evidence, s.dec.CorrectionPenalty = nil, nil // 依位置的證據以音節為單位；切法只看詞庫與語言模型
		search := searchCopy(s.nineSearch[begin])
		searches[begin] = search
		// 切法只看完整的詞與完整的音節：不算尾端「更長的詞的前綴」（CV300 三星式整串不按聲調鍵，算前綴時 161/300、
		// 不算 162/300，每個請求的時間卻多一倍），最後的鍵也不展開成還沒打完的音節（nineGraph；最後幾顆鍵怎麼切等
		// 下一顆鍵再說，每一鍵的時間少一半）。顯示用的解碼（decodeWithLiterals）照舊在最後的音節展開、算前綴。
		path, _ := s.dec.BestSearch(search, v.lat, locks, false)
		ends := map[int]bool{}
		for _, g := range path {
			if g.Cuts == nil {
				for u := g.Start + 1; u <= g.End; u++ {
					ends[u] = true
				}
			} else {
				for _, u := range g.Ends() {
					ends[u] = true
				}
			}
		}
		for p := begin; p < end; {
			if !s.inRun(p) {
				p++
				continue
			}
			q := p
			for q < end && s.inRun(q) {
				q++
			}
			u0, u1 := v.unit[p-begin], v.unit[q-begin]
			var cuts []int
			if len(path) > 0 {
				for u := u0 + 1; u <= u1; u++ {
					if ends[u] {
						cuts = append(cuts, u-u0)
					}
				}
			} else {
				cuts = s.localCuts(p, q, cursor) // 解不出路徑
			}
			runs = append(runs, runCut{p, q, cuts})
			p = q
		}
		begin = end
	}
	s.nineSearch = searches
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		s.replaceRun(r.a, r.b, s.runSyllables(r.a, r.b, r.cuts))
	}
	s.cursor = s.atKeyCoord(cursor)
	s.syncNine()
}

// localCuts 是按鍵圖解不出路徑時 [p, q) 這段 run 的切法（run 內的鍵座標）：照目前的切法，只把按鍵圖上沒有讀音的
// 音節（剛接上鍵的那個，例如超過 3 顆鍵）就地切開、把只能是一個音節的地方（wholeSpans）合起來，其他音節不動。
// 「有讀音」跟按鍵圖同一條規則（spanRows）：還沒打完的拼法只有 run 的最後一個、沒按聲調鍵的音節可以展開，其他位置
// 要組得成完整音節（或是一顆鍵）。
func (s *Session) localCuts(p, q, cursor int) []int {
	keys, stops := s.runStops(p, q, cursor)
	at := map[int]bool{}
	n := 0
	for r := p; r < q; r++ {
		syl := s.nineSeg[r]
		w := len(syl.keys)
		tail := r == q-1 && syl.anchor < 0 && !s.inRun(q)
		if w <= 3 && len(s.spanRows(keys[n:n+w], syl.tone(), tail).readings) > 0 {
			at[n+w] = true
		} else {
			for _, c := range s.greedyCuts(keys[n : n+w]) {
				at[n+c] = true
			}
		}
		n += w
	}
	for c := range stops {
		at[c] = true
	}
	for _, w := range s.wholeSpans(keys, stops) {
		for c := w[0] + 1; c < w[1]; c++ {
			delete(at, c)
		}
	}
	var cuts []int
	for c := 1; c <= n; c++ {
		if at[c] {
			cuts = append(cuts, c)
		}
	}
	return cuts
}

// nineSegReading 是 decode.Reading(s.dec.Lex, lat, start, end, word)（absorbNine 每次 refresh 對路徑上每個詞算兩次）。
// 自動切字時記下來：九宮格音節的列來自 nineRowCache，同一串鍵是同一份列，所以用列的位址當 key（連同列本身存著，
// 位址不會被別的列重用），再加上詞庫的版本（自訂詞彙、學習）。其他時候照算。
func (s *Session) nineSegReading(lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, bool) {
	if !s.NineSegment || len(s.nineSeg) == 0 {
		return decode.Reading(s.dec.Lex, lat, start, end, word)
	}
	owner, version := s.model.PriorVersion()
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%p|%d|", word, s.model.Gen, owner, version)
	for _, row := range lat[start:end] {
		fmt.Fprintf(&b, "%p:%d,", row, len(row)) // %p 是第一個元素的位址
	}
	key := b.String()
	if r, ok := s.readingMemo[key]; ok {
		return r.reading, r.ok
	}
	reading, ok := decode.Reading(s.dec.Lex, lat, start, end, word)
	if s.readingMemo == nil || len(s.readingMemo) > 4096 {
		s.readingMemo = map[string]readingMemo{}
	}
	s.readingMemo[key] = readingMemo{slices.Clone(lat[start:end]), reading, ok}
	return reading, ok
}

// readingMemo 是 nineSegReading 記下的一筆；rows 讓 key 裡的位址一直有效。
type readingMemo struct {
	rows    input.Lattice
	reading []zhuyin.Syllable
	ok      bool
}

// cutSignature 是決定切法的狀態：同顯示的簽章，不含游標，九宮格音節不看顯示用的讀音（解碼之後會換成路徑用的那個），
// 只看鍵、錨點與固定的拼法（nineSegSignature）。
func (s *Session) cutSignature() string {
	var b strings.Builder
	for p, y := range s.syls {
		if _, ok := s.nineSeg[p]; ok {
			b.WriteString("#")
		} else {
			b.WriteString(string(y))
		}
		b.WriteByte(' ')
	}
	cursor := s.cursor
	s.cursor = -1
	defer func() { s.cursor = cursor }()
	sig := s.signature()
	if i := strings.IndexByte(sig, '|'); i >= 0 {
		sig = sig[i:] // 前面是 zhuyin.Key(s.syls)
	}
	return b.String() + sig
}

// nineSegTouches 回報 [start, end) 有沒有還沒固定的九宮格音節。
func (s *Session) nineSegTouches(start, end int) bool {
	for p := start; p < end; p++ {
		if s.inRun(p) {
			return true
		}
	}
	return false
}

// nineSegment 是 [start, end) 所在的那段組字（字面文字之間）的範圍。
func (s *Session) nineSegment(start, end int) (int, int) {
	begin, stop := start, end
	for begin > 0 && !s.literals[begin-1] {
		begin--
	}
	for stop < len(s.syls) && !s.literals[stop] {
		stop++
	}
	return begin, stop
}

// nineLocks 是 [begin, end) 的鎖定換成按鍵圖的位置。
func (s *Session) nineLocks(v nineView, end int) map[int]decode.Lock {
	locks := map[int]decode.Lock{}
	for at, l := range s.locksIn(v.begin, end) {
		l.Len = v.unit[at+l.Len] - v.unit[at]
		locks[v.unit[at]] = l
	}
	return locks
}

// nineCandidates 是候選範圍蓋到還沒固定的九宮格音節時的候選：在按鍵圖上列 [pos, pos+length) 那幾顆鍵的所有詞，
// 字數可以跟目前的音節數不同（ㄒㄧㄢ 是「先」也可以是「西安」）。依位置的證據以音節為單位，這裡不用。
// singles 時只列一個字的詞（混合清單接在詞後面的單字，見 appendSingles）：一個音節的鍵在按鍵圖上也排得進多字的詞
// （ㄒㄧㄢ 的「西安」），那些已經在詞長的清單裡，單字段不重複列，也不必逐一評分。
func (s *Session) nineCandidates(pos, length int, pinned func(string) bool, singles bool) []decode.Candidate {
	begin, end := s.nineSegment(pos, pos+length)
	v := s.nineGraphIn(s.lattice(), begin, end, -1, pos, pos+length) // 只有候選範圍換成鍵
	locks := s.nineLocks(v, end)
	s.configureDocumentContext(begin)
	s.dec.Evidence, s.dec.CorrectionPenalty = nil, nil
	start, stop := v.unit[pos-begin], v.unit[pos+length-begin]
	w := decode.Window{Lat: v.lat, Pinned: pinned}
	if singles {
		w.Accept = func(e lexicon.Entry) bool { return len(e.Reading) == 1 }
	}
	return s.dec.CandidatesInContextWindows(v.lat, start, stop-start, locks, w)[0]
}

// nineMaterialize 把 [start, start+length) 照讀音 reading 切開並固定：還沒固定的九宮格音節換成 reading 走的切法，
// 每個音節固定成 reading 那個音節的拼法。回傳新的音節數；reading 放不進那幾顆鍵時回傳 false，什麼都不改。
func (s *Session) nineMaterialize(start, length int, reading []zhuyin.Syllable) (int, bool) {
	begin, end := s.nineSegment(start, start+length)
	v := s.nineGraphIn(s.lattice(), begin, end, -1, start, start+length) // 同候選（nineCandidates）的圖
	from, to := v.unit[start-begin], v.unit[start+length-begin]
	var cuts []int
	var walk func(u, k int, acc []int) bool
	walk = func(u, k int, acc []int) bool {
		if k == len(reading) {
			if u == to {
				cuts = acc
				return true
			}
			return false
		}
		if u >= to {
			return false
		}
		for _, h := range v.lat[u] {
			if h.Syl == reading[k] && u+h.Width() <= to && walk(u+h.Width(), k+1, append(acc[:len(acc):len(acc)], u+h.Width())) {
				return true
			}
		}
		return false
	}
	if !walk(from, 0, nil) {
		return 0, false
	}
	// 只換 run 的部分；其他位置（固定過的、一般的音節）各是一個位置，切點必然落在它們的兩端。
	type part struct {
		a, b int
		syls []nineSyl
	}
	var parts []part
	k := 0 // cuts 裡已經走過的切點數
	for p := start; p < start+length; {
		if !s.inRun(p) {
			p++
			continue
		}
		q := p
		for q < start+length && s.inRun(q) {
			q++
		}
		u0, u1 := v.unit[p-begin], v.unit[q-begin]
		var local []int
		for ; k < len(cuts) && cuts[k] <= u1; k++ {
			if cuts[k] > u0 {
				local = append(local, cuts[k]-u0)
			}
		}
		syls := s.runSyllables(p, q, local)
		first := k - len(local)
		for i := range syls {
			syls[i].fixed = reading[first+i].Base()
		}
		parts = append(parts, part{p, q, syls})
		p = q
	}
	cursor := s.keyCoord(s.cursor)
	n := length
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		s.dropLocksAt(p.a, p.b)
		s.replaceRun(p.a, p.b, p.syls)
		n += len(p.syls) - (p.b - p.a)
	}
	s.cursor = s.atKeyCoord(cursor)
	s.syncNine()
	s.cand = nil
	return n, true
}

// nineSegOptions 是第 at 個九宮格音節可以選的拼法：從它的第一顆鍵起 1–3 顆鍵（含後面還沒固定的九宮格音節的鍵，
// 不跨過錨點）組得出的完整音節，每一項是畫面寫法與鍵數。目前的鍵數排前面，其餘照鍵數；同鍵數依常用程度。
func (s *Session) nineSegOptions(at int) (readings []zhuyin.Syllable, keys []int) {
	n := s.nineSeg[at]
	avail := slices.Clone(n.keys)
	anchors := map[int]int{}
	if n.anchor >= 0 {
		anchors[len(avail)] = n.anchor
	}
	for p := at + 1; p < len(s.syls) && s.inRun(p) && n.anchor < 0 && len(avail) < 3; p++ {
		m := s.nineSeg[p]
		avail = append(avail, m.keys...)
		if m.anchor >= 0 {
			anchors[len(avail)] = m.anchor
			break
		}
	}
	lengths := []int{len(n.keys)}
	for w := 1; w <= 3; w++ {
		if w != len(n.keys) {
			lengths = append(lengths, w)
		}
	}
	for _, w := range lengths {
		if w > len(avail) || w > 3 {
			continue
		}
		crossed := false
		for c := 1; c < w; c++ {
			_, crossed = anchors[c]
			if crossed {
				break
			}
		}
		if crossed {
			continue
		}
		tone := 0
		if t, ok := anchors[w]; ok {
			tone = max(t, 0)
		}
		for _, y := range s.nineReadings(avail[:w], tone) {
			readings, keys = append(readings, y), append(keys, w)
		}
	}
	return readings, keys
}

// fixNineSegReading 是自動切字時的 nine_reading：把第 to 個九宮格音節固定成 word（畫面寫法），佔 length 顆鍵
// （0 是目前的鍵數）。佔的鍵比目前多時借用後面還沒固定的音節的鍵，比較少時剩下的鍵留給後面重新切。
// 還沒打完的尾端音節：word 是它的鍵的某個開頭拼法時，每顆鍵換成只有那個注音（同關閉時的 pending），不固定。
// word 不是那個位置、那個鍵數的讀音時什麼都不改，回傳 false。
func (s *Session) fixNineSegReading(to int, word string, length int) bool {
	n, ok := s.nineSeg[to]
	if !ok {
		return false
	}
	if length == 0 {
		length = len(n.keys)
	}
	if s.nineSegPartial(to) && length == len(n.keys) {
		if y := zhuyin.Syllable(word); slices.Contains(nineSpellings(n.keys, false), y) {
			keys := make([]string, 0, len(n.keys))
			for _, r := range word {
				keys = append(keys, string(r))
			}
			n.keys = keys
			s.putNineSyl(to, n)
			s.cand = nil
			return true
		}
	}
	readings, lengths := s.nineSegOptions(to)
	choice := -1
	for i, y := range readings {
		if y.Text() == word && lengths[i] == length {
			choice = i
			break
		}
	}
	if choice < 0 {
		return false
	}
	// 收集被用到的音節的鍵，換成：固定的音節＋剩下的鍵（照原本的音節，錨點跟著鍵）。
	var keys []string
	anchors := map[int]int{}
	q := to
	for len(keys) < length && q < len(s.syls) {
		m, ok := s.nineSeg[q]
		if !ok || q > to && m.fixed != "" {
			break
		}
		keys = append(keys, m.keys...)
		if m.anchor >= 0 {
			anchors[len(keys)] = m.anchor
		}
		q++
	}
	if len(keys) < length {
		return false
	}
	fixed := nineSyl{keys: slices.Clone(keys[:length]), anchor: -1, fixed: readings[choice].Base()}
	if t, ok := anchors[length]; ok {
		fixed.anchor = t
	}
	syls := []nineSyl{fixed}
	if rest := keys[length:]; len(rest) > 0 {
		r := nineSyl{keys: slices.Clone(rest), anchor: -1}
		if t, ok := anchors[len(keys)]; ok {
			r.anchor = t
		}
		syls = append(syls, r)
	}
	cursor := s.keyCoord(s.cursor)
	s.dropLocksAt(to, q)
	s.replaceRun(to, q, syls)
	s.cursor = s.atKeyCoord(cursor)
	s.syncNine()
	s.cand = nil
	return true
}

// nineSegSignature 是自動切字在解碼簽章裡的部分（每個九宮格音節的鍵、錨點、固定的拼法與是否為尾端）；
// 關閉時是空字串。
func (s *Session) nineSegSignature() string {
	if len(s.nineSeg) == 0 {
		return ""
	}
	var b strings.Builder
	for _, at := range slices.Sorted(maps.Keys(s.nineSeg)) {
		n := s.nineSeg[at]
		fmt.Fprintf(&b, "seg:%d:%s:%d:%s:%v;", at, strings.Join(n.keys, ","), n.anchor, n.fixed, s.nineSegTail(at))
	}
	return b.String()
}

// fixNineSegWord 是 fixWord 蓋到還沒固定的九宮格音節時：詞要放得進那幾個音節的鍵（任何切法，只算打的音），
// 照詞的讀音切開、固定，再跟選字一樣鎖定。放不進時整個還原，回傳 false。
func (s *Session) fixNineSegWord(start, length int, word string) bool {
	saved := *s
	s.syls, s.locks = slices.Clone(s.syls), maps.Clone(s.locks)
	s.literals, s.displayed = maps.Clone(s.literals), maps.Clone(s.displayed)
	s.rejected, s.autoLog = slices.Clone(s.rejected), slices.Clone(s.autoLog)
	fail := func() bool {
		*s = saved
		return false
	}
	s.finishUntoned() // 同 fixWord：未打完的注音先完成
	if !s.comp.Empty() || start+length > len(s.syls) {
		return fail()
	}
	for at := start; at < start+length; at++ {
		if s.literals[at] {
			return fail()
		}
	}
	begin, end := s.nineSegment(start, start+length)
	// 同候選（nineCandidates）的圖：只有 [start, start+length) 換成鍵，範圍兩端是切點。整段換成鍵時範圍外的錨點會讓
	// 範圍裡的鍵只能跟範圍外的鍵合成一個音節（wholeSpans），畫面上、候選上有的詞反而放不進。
	v := s.nineGraphIn(s.lattice(), begin, end, -1, start, start+length)
	from, to := v.unit[start-begin], v.unit[start+length-begin]
	fits, correction := s.dec.FitsIn(v.lat, from, to, word)
	if !fits {
		return fail()
	}
	reading, _, ok := decode.ReadingCuts(s.dec.Lex, v.lat, from, to, word)
	if !ok {
		return fail()
	}
	shown, auto := s.path, s.autoLog
	n, ok := s.nineMaterialize(start, length, reading)
	if !ok {
		return fail()
	}
	shown, auto, rejected := reshown(shown, auto, start, length, n, word)
	s.cand = nil
	s.lockSelectionShown(start, decode.Lock{Word: word, Len: n}, correction, shown, auto, &rejected)
	for _, g := range s.path {
		// 解碼可以用一個更長的詞包住鎖定的詞（鎖定只限制那幾個字，見 decode.allowed），同選字一樣算成功：
		// 畫面上那幾個字就是 word。候選列上列得出來的詞因此都 fix 得了（APP 的不變式，TestNineSegmentListedWordsFit）。
		if g.Start <= start && start+n <= g.End && g.Cuts == nil {
			if r := []rune(g.Word); len(r) == g.End-g.Start && string(r[start-g.Start:start+n-g.Start]) == word {
				return true
			}
		}
	}
	return fail()
}

// nineSegSnapshot 是自動切字時的左欄（快照的 nine_readings）：從左邊數來第一個還沒固定的九宮格音節。
// 還沒打完的尾端音節列它的鍵的開頭拼法（Pending）；其他列 nineSegOptions。全部固定時沒有。
func (s *Session) nineSegSnapshot() *proto.NineReadings {
	at := -1
	for p := range s.syls {
		if s.inRun(p) {
			at = p
			break
		}
	}
	if at < 0 {
		return nil
	}
	n := s.nineSeg[at]
	if s.nineSegPartial(at) {
		spellings := byPrior(nineSpellings(n.keys, false), s.startPrior)
		if len(spellings) == 0 {
			return nil
		}
		out := &proto.NineReadings{At: at, Pending: true, Key: s.nineKeyIndex(at)}
		for _, y := range spellings {
			out.Readings = append(out.Readings, string(y))
			out.Keys = append(out.Keys, len(n.keys))
		}
		return out
	}
	readings, keys := s.nineSegOptions(at)
	if len(readings) == 0 {
		return nil
	}
	out := &proto.NineReadings{At: at, Key: s.nineKeyIndex(at), Keys: keys}
	for i, y := range readings {
		out.Readings = append(out.Readings, y.Text())
		if y == s.syls[at] && out.Current == 0 && i > 0 {
			out.Current = i
		}
	}
	return out
}

// nineKeyIndex 是第 at 個音節之前有幾顆九宮格鍵（nine_segments.key）。
func (s *Session) nineKeyIndex(at int) int {
	k := 0
	for p := range min(at, len(s.syls)) {
		k += len(s.nineSeg[p].keys)
	}
	return k
}

// nineSegments 是快照的 nine_segments：自動切字時組字裡每個九宮格音節目前的切法。
func (s *Session) nineSegments() []proto.NineSegment {
	if !s.NineSegment || len(s.nineSeg) == 0 {
		return nil
	}
	var out []proto.NineSegment
	key := 0
	for p := range s.syls {
		n, ok := s.nineSeg[p]
		if !ok {
			continue
		}
		g := proto.NineSegment{At: p, Key: key, Keys: len(n.keys), Reading: s.syls[p].Text(), Anchor: n.anchor >= 0, Fixed: n.fixed != ""}
		if s.nineSegPartial(p) {
			g.Partial = true
			if spellings := byPrior(nineSpellings(n.keys, false), s.startPrior); len(spellings) > 0 {
				g.Reading = string(spellings[0])
			}
		}
		out = append(out, g)
		key += len(n.keys)
	}
	return out
}

// reshown 把選字（或 fix）前畫面上的路徑與自動修正換到 nineMaterialize 之後的位置：[start, start+oldLen) 這幾個音節
// 換成了 newLen 個（詞 word），前面的不動、後面的平移；跨進這個範圍的詞只留範圍外的部分（標成前綴），範圍本身是
// word 一段，反正會被這次選字取代（refreshSelection 的穩定只需要範圍外照畫面）。這個範圍裡的自動修正拿掉。
//
// 被換掉的詞（拒絕，給學習用）照原本的規則在選字前的位置找，再換到新的位置；音節數因此對不上字數（ㄒㄧㄢ 的「先」
// 換成「西安」，一個字佔兩個音節）的不記：學習照 Syls[Start:End] 取讀音，對不上的拒絕會記在錯的讀音上。
func reshown(path []decode.Seg, auto []AutoChange, start, oldLen, newLen int, word string) ([]decode.Seg, []AutoChange, []personal.RejectedSeg) {
	end, delta := start+oldLen, newLen-oldLen
	var rejected []personal.RejectedSeg
	for _, g := range path {
		if g.Start <= start && start < g.End && (g.Word != word || g.Start != start || g.End != end) {
			r := personal.RejectedSeg{Word: g.Word, Start: g.Start, End: g.End, Correction: g.Correction}
			if r.End < end {
				continue // 結束在範圍裡：新的切法沒有對應的邊界
			}
			r.End += delta
			if utf8.RuneCountInString(r.Word) == r.End-r.Start {
				rejected = append(rejected, r)
			}
		}
	}
	var out []decode.Seg
	placed := false
	place := func() {
		if !placed {
			out = append(out, decode.Seg{Word: word, Start: start, End: start + newLen})
			placed = true
		}
	}
	for _, g := range path {
		switch {
		case g.End <= start:
			out = append(out, g)
		case g.Start >= end:
			place()
			g.Start, g.End = g.Start+delta, g.End+delta
			out = append(out, g)
		default:
			runes := []rune(g.Word)
			if len(runes) != g.End-g.Start {
				place()
				continue
			}
			if g.Start < start {
				p := g
				p.End, p.Word, p.Partial, p.Matched = start, string(runes[:start-g.Start]), true, ""
				out = append(out, p)
			}
			place()
			if g.End > end {
				p := g
				p.Start, p.End, p.Word, p.Partial, p.Matched = end+delta, g.End+delta, string(runes[end-g.Start:]), true, ""
				out = append(out, p)
			}
		}
	}
	place()
	var kept []AutoChange
	for _, c := range auto {
		switch {
		case c.End <= start:
			kept = append(kept, c)
		case c.Start >= end:
			c.Start, c.End = c.Start+delta, c.End+delta
			kept = append(kept, c)
		}
	}
	return out, kept, rejected
}

// searchCopy 是逐鍵增量解碼狀態的一份新複本（寫時複製）：外殼會淺複製整個 session、在複本上試做（APP 的候選列先把
// 游標移到句首再查），複本解碼時不能改到原本那份，否則回到原本的 session 時增量狀態已經換成複本的音節格，下一鍵要從頭解。
// decode.Search 不改舊的內容，複製結構本身就夠了。
func searchCopy(old *decode.Search) *decode.Search {
	if old == nil {
		return &decode.Search{}
	}
	s := *old
	return &s
}
