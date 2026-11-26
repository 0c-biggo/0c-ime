package session

import (
	"maps"
	"slices"
	"sort"
	"strconv"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 合併弧（APP#25，接 #335；手機的觸控鍵盤，SpatialOptions.Swap、Repeat，零值關閉）：快打時打錯順序（ㄏㄠ 打成 ㄠㄏ、
// 聲調先打成 ㄏˇㄠ）、重複按了一鍵（ㄏㄏㄠ），聲調選填會把多出來的那一段切成另一個音節（ㄠ 是「奧」、ㄏ 是簡拼），
// 一個位置一個音節的音節格只能在原本的切法上換讀音，救不回來。這裡把相鄰兩、三個打出來的音節合成一個合法音節，當成帶成本的修正交給語言模型比較：
// 打的鍵不加成本，合併固定加成本，整句分數贏了才合。
//
// 做法：重切並固定，同九宮格自動切字（nine_segment.go 的 resegment）。
//   - 組字的單位仍然是音節，一個位置一個音節一個字。合成的位置在 syls 放合成的讀音（ㄏㄠ，沒打聲調時照樣帶
//     UntonedMark），merges 記著它是哪幾個打出來的音節、各自的鄰鍵讀音，拆回去時照原樣放回。所以畫面、游標、候選、
//     fix、送出、學習、神經評分與 ReadingAt 都照一般的音節走，協定的位置也還是字。
//   - 每次音節改變時（refresh 的開頭，remerge），在組字最後 mergeWindow 個打出來的音節上重新決定：把窗內合過的拆回去，
//     列出相鄰兩、三個音節合得成的讀音，分別解「都不合」與「合這幾個」（顯示用的同一個音節格與解碼器設定，只是不帶依位置
//     的暫時證據，同 resegment），整句分數高的寫回組字。窗以外、鎖定蓋到的照目前的樣子固定。
//   - 合成的位置在音節格裡只有合成的讀音（沒打聲調時五個聲調），成本是合併的成本、標 Corrected：路徑上的詞因此帶
//     Seg.Matched（decode.matchedReading），畫面標成修正、候選標 corrected，送出時用 Matched 的讀音學（同鄰鍵）。
//   - 退格：游標前是合成的音節時拆回去，刪掉最後那個（mergeBackspace），外殼照一般音節重打少一鍵的部分。
//
// 為什麼不在音節格放 input.Hyp.Span 2 的弧直接解：音節格一有 Span > 1 的假設，解碼器就改用按鍵圖的規則（Lattice.Width），
// 弧數上限看走過的假設、不看位置，鄰鍵讀音的弧不再裁切，跟顯示用的解碼不一樣；解出來的路徑一個詞的字數也跟
// 位置數不同，組字條、游標、候選與學習都要另外處理。分開解幾種切法只多幾次解碼，而且只在窗內真的有合得成的地方時才解
// （沒打錯的句子 CV300 只有幾個這種地方），用的是同一個逐鍵增量的解碼狀態（decode.Search），每種只重算窗內的位置。
//
// 只在聲調選填的音節上有意義：打了聲調的兩個音節合不成一個（兩個聲調），所以全打聲調的句子沒有合併的地方。
// 不放的地方：九宮格、字面文字、中英混打的按鍵、鎖定的詞；相鄰兩個只打聲母的簡拼，或簡拼旁邊又是簡拼（正在連續簡拼，
// runAbbreviated）。換鍵（Rejoin）另外不放在只打一鍵的音節連成的一串裡（只打首音，APP#36，見 mergeCandidates）。

type mergeKind uint8

const (
	mergeSwap   mergeKind = iota + 1 // 對調一組相鄰的鍵（ㄠ＋ㄏ → ㄏㄠ、ㄏˇ＋ㄠ → ㄏㄠˇ）
	mergeRepeat                      // 去掉重複按的一鍵（ㄏ＋ㄏㄠ → ㄏㄠ）
	mergeRejoin                      // 一鍵換成它的鄰鍵（ㄖ＋ㄧㄤ → ㄒㄧㄤ）
)

// mergeWindow：合併弧只放在最後這幾個打出來的音節（合成的音節照打出來的個數算）；更前面的照目前的樣子固定。同 missedWindow。
const mergeWindow = missedWindow

// maxMergeWidth：合併弧最多合幾個打出來的音節。對調一組鍵常把一個音節切成三段：ㄗㄨㄛ 打成 ㄨㄗㄛ 是 ㄨ＋ㄗ（簡拼）＋ㄛ。
const maxMergeWidth = 3

// maxMergeTrials：每次重新決定最多試幾種合法（每種一次整句解碼，只重算窗內），依成本由低到高。
const maxMergeTrials = 6

// typedSyl 是一個打出來的音節，連同它的鄰鍵讀音（Session.spatial）與每個注音的鄰鍵（Session.keyNear）。
type typedSyl struct {
	syl     zhuyin.Syllable
	spatial []input.Hyp
	keys    []spatialSlot
}

// mergedSyl 是合併弧合成的音節：syl 是合成的讀音（syls 那個位置的值），parts 是打出來的兩、三個音節，cost 是合併的成本。
type mergedSyl struct {
	syl   zhuyin.Syllable
	parts []typedSyl
	cost  float64
	kind  mergeKind
}

// mergeUnit 是窗內的一個位置：打出來的音節，或合成的音節（merged 不是 nil）。
type mergeUnit struct {
	typedSyl
	merged *mergedSyl
}

// mergeCand 是一種合法：把窗內從第 at 個起 width 個打出來的音節合成 syl。
type mergeCand struct {
	at, width int
	syl       zhuyin.Syllable
	cost      float64
	kind      mergeKind
}

// overlaps 回報兩種合法有沒有共用打出來的音節。
func (c mergeCand) overlaps(o mergeCand) bool { return c.at < o.at+o.width && o.at < c.at+c.width }

// mergedAt 是第 at 個位置的合成音節；syls 那個位置後來換成別的音節時不算。
func (s *Session) mergedAt(at int) (mergedSyl, bool) {
	m, ok := s.merges[at]
	return m, ok && at < len(s.syls) && s.syls[at] == m.syl
}

// TypedAt 回傳第 at 個音節打出來的注音（照畫面寫法）：合併弧合成的音節是打的那兩、三個音節，其他是它自己；at 不在
// 組字範圍時回傳 nil。外殼的退格與組字條照打的鍵數音節，要用它把核心的位置對回打的音節。
func (s *Session) TypedAt(at int) []string {
	if at < 0 || at >= len(s.syls) {
		return nil
	}
	if m, ok := s.mergedAt(at); ok {
		out := make([]string, len(m.parts))
		for i, p := range m.parts {
			out[i] = p.syl.Text()
		}
		return out
	}
	return []string{s.syls[at].Text()}
}

// widenMerged 把合成的位置換成合成的讀音（沒打聲調時五個聲調），成本是合併的成本，標 Corrected；沒打聲調的同時標
// Untoned，弧數照沒打聲調的位置裁切（同旁邊的鄰鍵讀音）。
func (s *Session) widenMerged(syls []zhuyin.Syllable, lat input.Lattice) input.Lattice {
	if len(s.merges) == 0 || len(syls) != len(s.syls) {
		return lat
	}
	for at, m := range s.merges {
		if at >= len(lat) || syls[at] != m.syl {
			continue
		}
		row := make([]input.Hyp, 0, 5)
		for _, y := range m.syl.Tones() {
			row = append(row, input.Hyp{Syl: y, Cost: m.cost, Corrected: true, Untoned: m.syl.Untoned()})
		}
		lat[at] = row
	}
	return lat
}

// mergeable 回報第 p 個位置能不能進合併弧的窗。
func (s *Session) mergeable(p int) bool {
	if s.literals[p] || len(s.nine[p]) > 0 || s.mixedKeys[p] != "" || s.literalKeys[p] != "" {
		return false
	}
	if _, ok := s.nineSeg[p]; ok {
		return false
	}
	for at, l := range s.locks {
		if at <= p && p < at+l.Len {
			return false
		}
	}
	return true
}

// mergeTail 是合併弧的窗：從 w0 到組字結尾，打出來的音節最多 mergeWindow 個。
func (s *Session) mergeTail() (int, []mergeUnit) {
	n := len(s.syls)
	w0, typed := n, 0
	for p := n - 1; p >= 0 && s.mergeable(p); p-- {
		w := 1
		if m, ok := s.mergedAt(p); ok {
			w = len(m.parts)
		}
		if typed+w > mergeWindow {
			break
		}
		typed += w
		w0 = p
	}
	units := make([]mergeUnit, 0, n-w0)
	for p := w0; p < n; p++ {
		u := mergeUnit{typedSyl: typedSyl{s.syls[p], s.spatial[p], s.keyNear[p]}}
		if m, ok := s.mergedAt(p); ok {
			u.merged = &m
		}
		units = append(units, u)
	}
	return w0, units
}

// splitMerged 把合成的音節拆回打出來的兩個。
func splitMerged(units []mergeUnit) []mergeUnit {
	out := make([]mergeUnit, 0, len(units)+2)
	for _, u := range units {
		if u.merged == nil {
			out = append(out, u)
			continue
		}
		for _, p := range u.merged.parts {
			out = append(out, mergeUnit{typedSyl: p})
		}
	}
	return out
}

// applyMerges 照 cs（彼此不重疊）把 plain 裡相鄰的幾個合成一個。
func applyMerges(plain []mergeUnit, cs ...mergeCand) []mergeUnit {
	at := map[int]mergeCand{}
	for _, c := range cs {
		at[c.at] = c
	}
	out := make([]mergeUnit, 0, len(plain))
	for k := 0; k < len(plain); {
		c, ok := at[k]
		if !ok || k+c.width > len(plain) {
			out = append(out, plain[k])
			k++
			continue
		}
		m := &mergedSyl{syl: c.syl, cost: c.cost, kind: c.kind}
		for _, u := range plain[k : k+c.width] {
			m.parts = append(m.parts, u.typedSyl)
		}
		out = append(out, mergeUnit{typedSyl: typedSyl{syl: c.syl}, merged: m})
		k += c.width
	}
	return out
}

// typedKeys 是打出 y 的鍵：注音照音節的順序（組音器本來就照「聲母 介音 韻母」排），打了聲調時最後是聲調鍵
// （一聲是 Tone1）。不是注音音節時 ok 為 false。
func typedKeys(y zhuyin.Syllable) (keys []rune, ok bool) {
	if !y.Untoned() && !y.Legal() {
		return nil, false
	}
	for _, r := range string(y.Base()) {
		if zhuyin.ClassOf(r) == zhuyin.None || zhuyin.ClassOf(r) == zhuyin.Tone {
			return nil, false
		}
		keys = append(keys, r)
	}
	if t := y.ToneOf(); t > 0 {
		keys = append(keys, []rune{zhuyin.Tone1, 'ˊ', 'ˇ', 'ˋ', '˙'}[t-1])
	}
	return keys, len(keys) > 0
}

// mergedReading 是一串鍵合成的音節：注音照順序是合法音節、聲調最多一個而且在最後；沒打聲調時是沒打聲調的音節。
// 沒有字的讀音不算。
func (s *Session) mergedReading(keys []rune) (zhuyin.Syllable, bool) {
	tone := 0
	body := make([]rune, 0, len(keys))
	for i, r := range keys {
		if zhuyin.ClassOf(r) == zhuyin.Tone {
			if i != len(keys)-1 {
				return "", false
			}
			tone = toneNumber(r)
			continue
		}
		body = append(body, r)
	}
	base := zhuyin.Syllable(string(body))
	if len(body) == 0 || !base.Legal() {
		return "", false
	}
	y := zhuyin.Untone(base)
	if tone > 0 {
		y = base.WithTone(tone)
	}
	for _, v := range y.Tones() {
		if s.syllablePrior(v) > 0 {
			return y, true
		}
	}
	return "", false
}

// mergeCandidates 列出窗內每一對相鄰的打出來的音節合得成的讀音，依成本由低到高。w0 是窗的開頭（看前一個音節是不是簡拼）。
func (s *Session) mergeCandidates(w0 int, plain []mergeUnit) []mergeCand {
	var out []mergeCand
	abbreviated := func(k int) bool {
		switch {
		case k < 0:
			return w0 > 0 && !s.literals[w0-1] && runAbbreviated(s.syls[w0-1])
		case k == len(plain):
			return s.pendingInitial() && len(plain) > 0 && plain[k-1].syl.ToneOf() == 0
		}
		return k < len(plain) && runAbbreviated(plain[k].syl)
	}
	// oneKeyRun 回報從第 k 個起 width 個打出來的音節連同兩邊，是不是都是只打一鍵的音節（ㄏ、ㄧ、ㄚ；組字開頭也算，
	// 結尾看還沒打完的是不是一個聲母，同 abbreviated）：只打首音的一串，換鍵的合併弧不放（APP#36）。只打首音時每個
	// 音節都是一鍵、每鍵都有好幾個鄰鍵，Rejoin 試的合法很多，卻沒有救回過一句（APP 的模擬：CV300 與 3000 句都是 0），
	// 只讓 M127F 只打首音的每鍵 P95 多 11–17 ms、清單中再打多 110 ms，還會把簡拼合成字數較少的音節。打聲調、不打聲調
	// 時打錯的音節旁邊幾乎都有兩鍵以上的音節，照樣放；只看組本身有沒有簡拼的話，打聲調時聲調鍵按到聲母（ˇ → ㄉ）產生的
	// 簡拼也擋掉，救回少很多。
	oneKeyRun := func(k, width int) bool {
		one := func(k int) bool {
			switch {
			case k < 0:
				if w0 == 0 {
					return true
				}
				ks, _ := typedKeys(s.syls[w0-1])
				return !s.literals[w0-1] && len(ks) == 1
			case k == len(plain):
				return s.pendingInitial()
			}
			ks, _ := typedKeys(plain[k].syl)
			return len(ks) == 1
		}
		for i := k - 1; i <= k+width; i++ {
			if !one(i) {
				return false
			}
		}
		return true
	}
	add := func(c mergeCand) {
		if i := slices.IndexFunc(out, func(o mergeCand) bool { return o.at == c.at && o.width == c.width && o.syl == c.syl }); i >= 0 {
			if c.cost < out[i].cost {
				out[i] = c
			}
			return
		}
		out = append(out, c)
	}
	for k := 0; k+1 < len(plain); k++ {
		for width := 2; width <= maxMergeWidth && k+width <= len(plain); width++ {
			group := plain[k : k+width]
			// 正在連續簡拼（ㄋㄏ）：相鄰兩個都是簡拼，或頭尾的簡拼旁邊又是簡拼，不是打錯。
			run := abbreviated(k) && abbreviated(k-1) || abbreviated(k+width-1) && abbreviated(k+width)
			for i := k; i+1 < k+width && !run; i++ {
				run = abbreviated(i) && abbreviated(i+1)
			}
			if run {
				continue
			}
			var keys []rune
			var near [][]spatialKey // 每個鍵的鄰鍵（聲調鍵沒有）
			ok := true
			for _, u := range group {
				ks, typed := typedKeys(u.syl)
				keys, ok = append(keys, ks...), ok && typed
				for _, r := range ks {
					var n []spatialKey
					if i := slices.IndexFunc(u.keys, func(k spatialSlot) bool { return k.sym == r }); i >= 0 {
						n = u.keys[i].near
					}
					near = append(near, n)
				}
			}
			if !ok {
				continue
			}
			if s.Spatial.Swap > 0 {
				for i := 0; i+1 < len(keys); i++ {
					if keys[i] == keys[i+1] {
						continue
					}
					t := slices.Clone(keys)
					t[i], t[i+1] = t[i+1], t[i]
					if y, ok := s.mergedReading(t); ok {
						add(mergeCand{k, width, y, s.Spatial.Swap, mergeSwap})
					}
				}
			}
			if s.Spatial.Repeat > 0 && !reduplicated(group) {
				for i := 1; i < len(keys); i++ {
					if keys[i] != keys[i-1] {
						continue
					}
					if y, ok := s.mergedReading(slices.Delete(slices.Clone(keys), i, i+1)); ok {
						add(mergeCand{k, width, y, s.Spatial.Repeat, mergeRepeat})
					}
				}
			}
			if s.Spatial.Rejoin > 0 && !oneKeyRun(k, width) {
				for i, ns := range near {
					for _, n := range ns {
						if zhuyin.ClassOf(n.sym) == zhuyin.Tone || n.cost > s.Spatial.maxCost() {
							continue
						}
						t := slices.Clone(keys)
						t[i] = n.sym
						if y, ok := s.mergedReading(t); ok {
							add(mergeCand{k, width, y, s.Spatial.Weight*n.cost + s.Spatial.Rejoin, mergeRejoin})
						}
					}
				}
			}
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].cost < out[b].cost })
	return out
}

// pendingInitial 回報還沒打完的注音是不是只有一個聲母：只打首音時下一個音節多半就是它（ㄨㄅㄍ 打到 ㄍ 時，ㄅ 旁邊
// 也是簡拼），前面是沒打聲調的簡拼時算成簡拼；接著打了介音或韻母就不算，那時 refresh 再決定一次（簽章帶著它）。
// 打了聲調的簡拼（ㄠ＋ㄏˇ 接著打 ㄋ）不看它：不然打下一個字的聲母時合好的字會先拆開。
func (s *Session) pendingInitial() bool {
	r := []rune(s.comp.String())
	return len(r) == 1 && zhuyin.ClassOf(r[0]) == zhuyin.Initial
}

// reduplicated 回報 group 是不是同一個音節打了好幾次（疊字：ㄞ＋ㄞ 是「矮矮」、ㄛ＋ㄛ 是「喔喔」）：去掉一鍵就合成
// 一個音節，但多半是真的要打疊字，不放重複一鍵的合法。
func reduplicated(group []mergeUnit) bool {
	for _, u := range group[1:] {
		if u.syl != group[0].syl {
			return false
		}
	}
	return true
}

// runAbbreviated 回報 y 在「正在連續簡拼」的判斷裡算不算簡拼：只打一個聲母的音節。只打介音的（ㄧ、ㄨ、ㄩ）本身就是
// 完整音節（一、烏、迂），對調常把它切出來（ㄗㄨㄛ 打成 ㄨㄗㄛ 是 ㄨ＋ㄗ＋ㄛ），不算。APP 的模擬（CV300）裡把它算進去時
// 不打聲調的同音節對調只救回 124/208，不算是 180/208；只打首音的輸入有 1 句輸出不同（本來就錯的「請繼續維護」→「奇蹟修護」）。
func runAbbreviated(y zhuyin.Syllable) bool {
	return y.Abbreviated() && zhuyin.ClassOf([]rune(string(y.Base()))[0]) != zhuyin.Medial
}

// mergeSignature 是決定窗內合法的輸入：窗內全部拆開時的組字（同顯示的簽章）加上窗的位置與成本。合過之後拆開的樣子
// 不變，所以寫回之後再算一次還是同一個，不會一直重解。
func (s *Session) mergeSignature(w0 int, plain []mergeUnit) string {
	syls, cursor := s.syls, s.cursor
	t := slices.Clone(syls[:w0])
	for _, u := range plain {
		t = append(t, u.syl)
	}
	s.syls, s.cursor = t, len(t)
	sig := s.signature()
	s.syls, s.cursor = syls, cursor
	return strconv.Itoa(w0) + "|" + strconv.FormatBool(s.pendingInitial()) + "|" + strconv.FormatFloat(s.Spatial.Swap, 'g', -1, 64) + "|" + strconv.FormatFloat(s.Spatial.Repeat, 'g', -1, 64) + "|" + strconv.FormatFloat(s.Spatial.Rejoin, 'g', -1, 64) + "|" + sig
}

// tailIs 回報從 w0 起的組字是不是已經是 units。
func (s *Session) tailIs(w0 int, units []mergeUnit) bool {
	if len(s.syls)-w0 != len(units) {
		return false
	}
	for i, u := range units {
		m, merged := s.mergedAt(w0 + i)
		if s.syls[w0+i] != u.syl || merged != (u.merged != nil) || merged && !m.sameParts(*u.merged) {
			return false
		}
	}
	return true
}

// setTail 把從 w0 起的組字換成 units（游標在結尾）：照一般的刪除、插入平移各種依位置的紀錄，鄰鍵讀音跟著打的音節。
func (s *Session) setTail(w0 int, units []mergeUnit) {
	if s.tailIs(w0, units) {
		return
	}
	for len(s.syls) > w0 {
		s.eraseBeforeCursor()
	}
	for _, u := range units {
		at := s.cursor
		s.pendingSpatial, s.pendingKeys = u.spatial, u.keys
		s.insert(u.syl)
		if u.merged != nil {
			s.merges = maps.Clone(s.merges) // 存起來準備還原的那份不跟著改
			if s.merges == nil {
				s.merges = map[int]mergedSyl{}
			}
			s.merges[at] = *u.merged
		}
	}
}

// sameParts 回報兩個合成的音節是不是同樣的幾個打出來的音節合的（鄰鍵讀音跟著打的音節，比音節就夠）。
func (m mergedSyl) sameParts(o mergedSyl) bool {
	return m.cost == o.cost && slices.EqualFunc(m.parts, o.parts, func(a, b typedSyl) bool { return a.syl == b.syl })
}

// remerge 重新決定窗內哪幾對打出來的音節要合成一個（見檔頭）。只在游標在結尾、候選沒開時做；窗內沒有合過也合不起來的
// 地方時什麼都不做。
func (s *Session) remerge() {
	if !s.Spatial.merging() || s.cand != nil || s.cursor != len(s.syls) || len(s.syls) == 0 {
		return
	}
	w0, units := s.mergeTail()
	plain := splitMerged(units)
	cands := s.mergeCandidates(w0, plain)
	if len(cands) == 0 && len(plain) == len(units) {
		return
	}
	sig := s.mergeSignature(w0, plain)
	if sig == s.mergeSig {
		return
	}
	defer func() { s.mergeSig = sig }()
	if len(cands) == 0 {
		s.setTail(w0, plain)
		return
	}
	begin := w0
	for begin > 0 && !s.literals[begin-1] {
		begin--
	}
	locks := s.locksIn(begin, w0) // 鎖定都在窗的前面，換窗內的音節不影響它們
	s.dec.Beam, s.dec.Continue = s.Opt.Beam, s.Opt.Continue
	s.dec.SeparateContinuation = s.Opt.SeparateContinuation
	s.dec.PrefixCacheKeys = s.Opt.PrefixCacheKeys
	evidence, penalty := s.dec.Evidence, s.dec.CorrectionPenalty
	defer func() { s.dec.Evidence, s.dec.CorrectionPenalty = evidence, penalty }()
	s.configureDocumentContext(begin)
	s.dec.Evidence, s.dec.CorrectionPenalty = nil, nil // 同 resegment：依位置的證據以目前的音節為單位，合法只看詞庫與語言模型
	search := searchCopy(s.mergeSearch)
	s.mergeSearch = search
	score := func(units []mergeUnit) float64 {
		s.setTail(w0, units)
		_, sc := s.dec.BestSearch(search, s.lattice()[begin:], locks, s.Opt.PrefixAware)
		return sc
	}
	base := score(plain)
	type trial struct {
		c     mergeCand
		score float64
	}
	var good []trial
	for _, c := range cands[:min(len(cands), maxMergeTrials)] {
		if sc := score(applyMerges(plain, c)); sc > base {
			good = append(good, trial{c, sc})
		}
	}
	sort.SliceStable(good, func(a, b int) bool { return good[a].score > good[b].score })
	var chosen []mergeCand
	best := base
	for _, t := range good {
		if slices.ContainsFunc(chosen, t.c.overlaps) {
			continue // 跟已經合的共用音節
		}
		next := append(slices.Clip(chosen), t.c)
		sc := t.score
		if len(chosen) > 0 {
			sc = score(applyMerges(plain, next...))
		}
		if sc > best {
			chosen, best = next, sc
		}
	}
	s.setTail(w0, applyMerges(plain, chosen...))
}

// mergeBackspace：游標前是合成的音節時，拆回打出來的音節，刪掉最後那個；外殼照一般音節的退格重打它少一鍵的部分
// （打的鍵數跟拆開之後的音節對得上）。不是合成的音節時回傳 false。
func (s *Session) mergeBackspace() bool {
	if s.cursor == 0 {
		return false
	}
	at := s.cursor - 1
	m, ok := s.mergedAt(at)
	if !ok {
		return false
	}
	s.eraseBeforeCursor()
	for _, p := range m.parts[:len(m.parts)-1] {
		s.pendingSpatial, s.pendingKeys = p.spatial, p.keys
		s.insert(p.syl)
	}
	return true
}
