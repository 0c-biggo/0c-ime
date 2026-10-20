package session

import (
	"slices"
	"strings"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/personal"
)

// applyPronounHabit 讓顯示路徑的代名詞性別只依使用者習慣（見 personal/pronoun.go）：
// 統計解碼或神經選字因語境選了「她」「妳」而沒有習慣支持時改回中性字，
// 有習慣支持而顯示中性字時改成變體。本次組字中手選的位置不動。神經次佳提示同樣處理，
// 只差在代名詞性別的提示不出現。
func (s *Session) applyPronounHabit() {
	s.path = s.pronounPath(s.path)
	h := s.hint
	if h == nil || len(h.segs) == 0 {
		return
	}
	start, end := h.segs[0].Start, h.segs[len(h.segs)-1].End
	var alt []decode.Seg
	for _, g := range s.path {
		if g.End <= start {
			alt = append(alt, g)
		}
	}
	alt = append(alt, h.segs...)
	for _, g := range s.path {
		if g.Start >= end {
			alt = append(alt, g)
		}
	}
	alt = s.pronounPath(alt)
	var segs []decode.Seg
	var text, shown strings.Builder
	for _, g := range alt {
		if g.Start >= start && g.End <= end {
			segs = append(segs, g)
			text.WriteString(g.Word)
		}
	}
	for _, g := range s.path {
		if g.Start >= start && g.End <= end {
			shown.WriteString(g.Word)
		}
	}
	if text.String() == shown.String() {
		s.hint = nil
		return
	}
	h.segs, h.text, h.replace = segs, text.String(), shown.String()
}

// pronounPath 回傳依習慣調整代名詞後的路徑；沒有變動時回傳原切片。
func (s *Session) pronounPath(path []decode.Seg) []decode.Seg {
	var segs []personal.Seg
	var index []int
	for i, g := range path {
		if !s.literals[g.Start] {
			segs = append(segs, personal.Seg{Word: g.Word, Start: g.Start, End: g.End, Partial: g.Partial})
			index = append(index, i)
		}
	}
	out, cloned := path, false
	for _, site := range s.model.PronounSites(segs) {
		g := segs[site.Seg]
		if s.lockedAt(g.Start + site.Offset) {
			continue
		}
		if s.model.PronounVariant(site.Neutral, site.Cues) == site.Variant {
			continue
		}
		if !cloned {
			out, cloned = slices.Clone(path), true
		}
		i := index[site.Seg]
		out[i].Word = personal.SwapPronoun(out[i].Word, site.Offset)
	}
	return out
}

func (s *Session) lockedAt(pos int) bool {
	for at, lock := range s.locks {
		if at <= pos && pos < at+lock.Len {
			return true
		}
	}
	return false
}

// orderPronounCandidates 讓候選視窗中同組代名詞的兩種寫法依習慣排列：沒有習慣時中性字在前，
// 免得開候選直接確認就選到語境猜的「她」。使用者選過而釘選的字不移動。
func (s *Session) orderPronounCandidates(items []decode.Candidate, pos, length int, pinned func(string) bool) {
	var segs []personal.Seg
	for _, g := range s.path {
		if !s.literals[g.Start] && (g.End <= pos || g.Start >= pos+length) {
			segs = append(segs, personal.Seg{Word: g.Word, Start: g.Start, End: g.End, Partial: g.Partial})
		}
	}
	at := map[string]int{}
	for i, c := range items {
		at[c.Entry.Surface] = i
	}
	for i := range items {
		w := items[i].Entry.Surface
		probe := append(slices.Clone(segs), personal.Seg{Word: w, Start: pos, End: pos + length})
		for _, site := range s.model.PronounSites(probe) {
			if site.Seg != len(probe)-1 {
				continue
			}
			j, ok := at[personal.SwapPronoun(w, site.Offset)]
			if !ok || j < i || pinned(w) {
				break // 只從排在前面的那一個處理
			}
			if s.model.PronounVariant(site.Neutral, site.Cues) != site.Variant {
				items[i], items[j] = items[j], items[i]
				at[items[i].Entry.Surface], at[items[j].Entry.Surface] = i, j
			}
			break
		}
	}
}
