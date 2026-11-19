package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 觸控鍵盤的相鄰鍵容錯（手機）：外殼每次點按依觸控位置送來鄰鍵與成本（proto.Request.Near），
// 核心在完成的音節旁放入「其中一鍵換成鄰鍵」的讀音，由語言模型決定（觸控鍵盤常見的空間模型）；
// 不打聲調時，打的鍵接不上目前的音節、而最可能的鄰鍵接得上，就改用鄰鍵，不把音節拆開。

// SpatialOptions 由宿主設定。Weight 是鄰鍵成本乘上的權重，0 關閉整個功能（桌面不送 Near，也一樣）。
// Penalty 是換成鄰鍵的固定成本，加在乘過權重的成本上：打到的鍵永遠不加成本，修正要明顯勝出才會取代它
// （文獻上避免把罕用詞改錯的做法，Ouyang 2017、Fowler 2015）。
// MaxCost 以上（乘過權重之前）的鄰鍵不用，免得音節格太大；0 表示 DefaultSpatialMaxCost。
type SpatialOptions struct {
	Weight  float64
	Penalty float64
	MaxCost float64
}

// DefaultSpatialMaxCost：比打到的鍵多 8 個自然對數單位以上的鄰鍵，機率不到三千分之一，不值得解碼。
const DefaultSpatialMaxCost = 8.0

func (o SpatialOptions) on() bool { return o.Weight > 0 }

func (o SpatialOptions) maxCost() float64 {
	if o.MaxCost > 0 {
		return o.MaxCost
	}
	return DefaultSpatialMaxCost
}

// spatialKey 是組音中一個注音的鄰鍵。
type spatialKey struct {
	sym  rune
	cost float64
}

func (s *Session) nearKeys(near []proto.NearKey) []spatialKey {
	if !s.Spatial.on() {
		return nil
	}
	var out []spatialKey
	for _, n := range near {
		r := []rune(n.Symbol)
		if len(r) != 1 || zhuyin.ClassOf(r[0]) == zhuyin.None || n.Cost < 0 || n.Cost > s.Spatial.maxCost() {
			continue
		}
		out = append(out, spatialKey{r[0], n.Cost})
	}
	slices.SortStableFunc(out, func(a, b spatialKey) int {
		switch {
		case a.cost < b.cost:
			return -1
		case a.cost > b.cost:
			return 1
		}
		return 0
	})
	return out
}

// spatialSubstitute 在打的鍵放不進組音時找最可能的修正：把打的鍵換成鄰鍵，或把組音裡前面某一鍵換成它的鄰鍵
// （ㄒ＋ㄚ 接不上，ㄒ 的鄰鍵 ㄏ 接得上：哈）。回傳要放進組音的鍵與它的鄰鍵；換的是前面的鍵時直接改掉組音。
// 沒有可行的修正時 ok 為 false，照原本的規則處理。
func (s *Session) spatialSubstitute(sym rune, near []spatialKey) (rune, []spatialKey, bool) {
	// 目前的組音自己就是完整音節時（ㄕ 是「使」），接不上的鍵是下一個音節的開頭（使用 ㄕ＋ㄩㄥ），不是按錯。
	alone := zhuyin.Syllable(s.comp.String()).Legal()
	misfit := zhuyin.ClassOf(sym) == zhuyin.Tone && s.comp.Empty() || s.toneless() && !alone && s.untonedBreak(sym) && !s.breakIsNew(sym)
	if !misfit {
		return sym, near, false
	}
	fits := func(r rune) bool {
		if zhuyin.ClassOf(r) == zhuyin.Tone {
			return !s.comp.Empty()
		}
		return !s.untonedBreak(r)
	}
	bestCost, bestSym := -1.0, rune(0)
	for _, n := range near {
		if fits(n.sym) {
			bestCost, bestSym = n.cost, n.sym
			break // 已依成本排序
		}
	}
	// 換掉組音裡前面的一鍵（同一類的鄰鍵），打的鍵照舊。
	var fixClass zhuyin.Class
	var fixSym rune
	if zhuyin.ClassOf(sym) != zhuyin.Tone {
		saved := s.comp
		for cl, slot := range s.compNear {
			if cl == zhuyin.Tone || saved.Slot(cl) != slot.sym {
				continue
			}
			for _, n := range slot.near {
				if zhuyin.ClassOf(n.sym) != cl || bestCost >= 0 && n.cost >= bestCost {
					continue
				}
				trial := saved.Clone()
				trial.Replace(cl, n.sym)
				s.comp = trial
				ok := fits(sym)
				s.comp = saved
				if ok {
					bestCost, bestSym, fixClass, fixSym = n.cost, 0, cl, n.sym
				}
			}
		}
	}
	switch {
	case bestCost < 0:
		return sym, near, false
	case bestSym == 0:
		old := s.compNear[fixClass]
		var rest []spatialKey
		for _, m := range old.near {
			if m.sym != fixSym {
				rest = append(rest, spatialKey{m.sym, max(0, m.cost-bestCost)})
			}
		}
		s.comp.Replace(fixClass, fixSym)
		s.compNear[fixClass] = spatialSlot{fixSym, rest}
		return sym, near, true
	}
	var rest []spatialKey
	for _, m := range near {
		if m.sym != bestSym {
			rest = append(rest, spatialKey{m.sym, max(0, m.cost-bestCost)})
		}
	}
	return bestSym, rest, true
}

// breakIsNew：打的鍵本來就該開始新的音節（目前的音節已有同一類或更後面的注音，例如打完 ㄏㄠ 再打 ㄋ），
// 不是「接不上」。只有接在後面卻不成音節（ㄏ＋ㄦ）才算可能按錯。
func (s *Session) breakIsNew(sym rune) bool {
	cl := zhuyin.ClassOf(sym)
	return s.comp.Slot(cl) != 0 || lastClass(s.comp) >= cl
}

// noteSpatial 記下剛放進組音的注音的鄰鍵。
func (s *Session) noteSpatial(sym rune, near []spatialKey) {
	if s.compNear == nil {
		s.compNear = map[zhuyin.Class]spatialSlot{}
	}
	s.compNear[zhuyin.ClassOf(sym)] = spatialSlot{sym, near}
}

type spatialSlot struct {
	sym  rune
	near []spatialKey
}

// spatialAlternatives 是剛完成的音節換掉一鍵的合法讀音與成本。before 是完成前的組音狀態（打聲調完成時不含聲調）。
func (s *Session) spatialAlternatives(before zhuyin.Composer, done zhuyin.Syllable) []input.Hyp {
	if !s.Spatial.on() || len(s.compNear) == 0 || done.Abbreviated() {
		return nil
	}
	slots := map[zhuyin.Class]rune{}
	for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
		if r := before.Slot(cl); r != 0 {
			slots[cl] = r
		}
	}
	untoned := done.Untoned()
	tone := done.ToneOf()
	build := func(slots map[zhuyin.Class]rune, tone int) (zhuyin.Syllable, bool) {
		var b []rune
		for cl := zhuyin.Initial; cl <= zhuyin.Final; cl++ {
			if r := slots[cl]; r != 0 {
				b = append(b, r)
			}
		}
		base := zhuyin.Syllable(string(b))
		if len(b) == 0 || !base.Legal() {
			return "", false
		}
		if untoned {
			return zhuyin.Untone(base), true
		}
		return base.WithTone(tone), true
	}
	var out []input.Hyp
	add := func(y zhuyin.Syllable, cost float64) {
		if y == done || cost > s.Spatial.maxCost() {
			return
		}
		for _, v := range y.Tones() {
			if !slices.ContainsFunc(out, func(h input.Hyp) bool { return h.Syl == v }) {
				// 換掉一鍵的讀音是修正、不是打的音：不標 Untoned（同打了聲調的音節），組字與候選標 corrected，
				// Fits、decode.Reading 與近音清單都把它當成近音。沒打聲調時照樣展開成五個聲調（y.Tones）。
				out = append(out, input.Hyp{Syl: v, Cost: s.Spatial.Weight*cost + s.Spatial.Penalty})
			}
		}
	}
	for cl, slot := range s.compNear {
		if cl == zhuyin.Tone {
			if untoned {
				continue
			}
			for _, n := range slot.near {
				if t := toneNumber(n.sym); t > 0 {
					if y, ok := build(slots, t); ok {
						add(y, n.cost)
					}
				}
			}
			continue
		}
		if slots[cl] != slot.sym {
			continue // 這一格後來換過別的注音
		}
		for _, n := range slot.near {
			ncl := zhuyin.ClassOf(n.sym)
			if ncl == zhuyin.Tone || ncl != cl && slots[ncl] != 0 {
				continue
			}
			alt := map[zhuyin.Class]rune{}
			for k, v := range slots {
				alt[k] = v
			}
			delete(alt, cl)
			alt[ncl] = n.sym
			if y, ok := build(alt, tone); ok {
				add(y, n.cost)
			}
		}
	}
	return out
}

func toneNumber(r rune) int {
	switch r {
	case zhuyin.Tone1:
		return 1
	case 'ˊ':
		return 2
	case 'ˇ':
		return 3
	case 'ˋ':
		return 4
	case '˙':
		return 5
	}
	return 0
}

// widenSpatial 在音節格放入各位置記下的鄰鍵讀音。
func (s *Session) widenSpatial(syls []zhuyin.Syllable, lat input.Lattice) input.Lattice {
	if len(s.spatial) == 0 || len(syls) != len(s.syls) {
		return lat
	}
	for at, alts := range s.spatial {
		if at >= len(lat) || s.literals[at] {
			continue
		}
		row := slices.Clone(lat[at])
		for _, h := range alts {
			if !slices.ContainsFunc(row, func(x input.Hyp) bool { return x.Syl == h.Syl }) {
				row = append(row, h)
			}
		}
		lat[at] = row
	}
	return lat
}

// labelSpatial 標出開著的候選裡、讀音要靠鄰鍵替換才得到的那些（proto.CandidateCorrected）。
func (s *Session) labelSpatial(pos int) {
	if len(s.spatial) == 0 || s.cand == nil {
		return
	}
	base := s.front.Expand(s.syls)
	for i, c := range s.cand.items {
		if i >= len(s.cand.labels) {
			break
		}
		for k, y := range c.Entry.Reading {
			at := pos + k
			if at >= len(base) || slices.ContainsFunc(base[at], func(h input.Hyp) bool { return h.Syl == y }) {
				continue
			}
			if slices.ContainsFunc(s.spatial[at], func(h input.Hyp) bool { return h.Syl == y }) {
				s.cand.labels[i] = proto.CandidateCorrected
				break
			}
		}
	}
}
