package session

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 手選重新計算鎖定與當次更正證據，但不能把未選取的字換回統計首選。
// 同讀音、同相鄰音節的重複更正仍沿用 localEvidence；其餘保留已顯示文字。
// 保留的是本次畫面，不新增手選鎖定；後續實際編輯照常重新解碼。
func (s *Session) refreshSelection(shown []decode.Seg, auto []AutoChange, selected decode.Seg) {
	s.refresh()
	if len(shown) == 0 {
		return
	}
	blocks := []autoBlock{{selected.Start, selected.End}}
	for begin := 0; begin < len(s.syls); {
		if s.literals[begin] {
			begin++
			continue
		}
		limit := begin + 1
		for limit < len(s.syls) && !s.literals[limit] {
			limit++
		}
		if evidence := s.localEvidence(begin); evidence != nil {
			left := ""
			for _, g := range s.path {
				if g.Start >= begin && g.End <= limit {
					mass, _ := evidence(g.Start-begin, g.End-begin, left, g.Word)
					if mass > 0 {
						blocks = append(blocks, autoBlock{g.Start, g.End})
					}
					left = g.Word
				}
			}
		}
		begin = limit
	}
	blocks = mergeBlocks(blocks)
	base, ok := s.splitSelectionPath(shown, blocks)
	if !ok {
		return
	}
	final := s.path
	if len(final) == 0 {
		final = []decode.Seg{selected} // 舊詞被切開且沒有片段詞條時仍保留畫面。
	}
	path, ok := splice(base, final, blocks)
	if !ok {
		return
	}
	s.path = path
	s.nearReading = false
	// 自動近音診斷必須對應實際畫面，不能留下被捨棄的解碼改動。
	kept := func(c AutoChange) bool {
		return !slices.ContainsFunc(blocks, func(b autoBlock) bool { return c.Start < b.to && b.from < c.End })
	}
	current := s.autoLog
	s.autoLog = nil
	for _, c := range auto {
		if kept(c) {
			s.autoLog = append(s.autoLog, c)
		}
	}
	for _, c := range current {
		if !kept(c) {
			s.autoLog = append(s.autoLog, c)
		}
	}
	s.hint = nil
	s.shownPath, s.shownSyls = slices.Clone(s.path), slices.Clone(s.syls)
	s.displayed = map[int]string{}
	for _, g := range s.path {
		for i, r := range []rune(g.Word) {
			if !s.literals[g.Start+i] {
				s.displayed[g.Start+i] = string(r)
			}
		}
	}
}

// 候選可能只取舊詞的一部分；切開舊顯示供 splice 使用，片段不當作完整詞學習。
func (s *Session) splitSelectionPath(path []decode.Seg, blocks []autoBlock) ([]decode.Seg, bool) {
	var out []decode.Seg
	for _, g := range path {
		if utf8.RuneCountInString(g.Word) != g.End-g.Start {
			return nil, false
		}
		cuts := []int{g.Start, g.End}
		for _, b := range blocks {
			for _, at := range []int{b.from, b.to} {
				if g.Start < at && at < g.End {
					cuts = append(cuts, at)
				}
			}
		}
		slices.Sort(cuts)
		cuts = slices.Compact(cuts)
		word, matched := []rune(g.Word), zhuyin.ParseReading(g.Matched)
		for i := 1; i < len(cuts); i++ {
			part := g
			part.Start, part.End = cuts[i-1], cuts[i]
			part.Word = string(word[part.Start-g.Start : part.End-g.Start])
			if part.Start != g.Start || part.End != g.End {
				part.Partial = true
				if len(matched) == g.End-g.Start {
					part.Matched = strings.TrimSpace(zhuyin.Key(matched[part.Start-g.Start : part.End-g.Start]))
					if slices.Equal(matched[part.Start-g.Start:part.End-g.Start], s.syls[part.Start:part.End]) {
						part.Matched = ""
					}
				}
				// 舊詞的 corrected／partial 標示不能套到本身已有完整原音詞條的片段。
				if part.Matched == "" {
					for _, entry := range s.model.Source().Exact(s.syls[part.Start:part.End]) {
						if entry.Surface == part.Word {
							part.Partial, part.Correction = false, entry.Correction
							if !entry.Correction {
								break
							}
						}
					}
				}
			}
			out = append(out, part)
		}
	}
	return out, true
}
