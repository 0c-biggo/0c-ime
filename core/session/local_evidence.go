package session

import (
	"sort"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// localEvidence 只從目前仍有效的手選鎖定重建證據，不寫入 model。
// 同讀音且左或右相鄰音節相同時提供一次暫時選取證據；不跨標點、不同欄位或 session。
// 刪除／改選／取消使鎖定失效時，證據自然消失；重播不會重複累計。
func (s *Session) localEvidence(base int) func(int, int, string, string) (float64, float64) {
	if s.paused || s.private || s.localBlocked || len(s.locks) == 0 {
		return nil
	}
	limit := base
	for limit < len(s.syls) && !s.literals[limit] {
		limit++
	}
	type evidence struct {
		start       int
		word        []rune
		reading     []zhuyin.Syllable
		left, right zhuyin.Syllable
		nextWord    string
		nextReading []zhuyin.Syllable
	}
	var entries []evidence
	for at, lock := range s.locks {
		if lock.Completion || lock.LiteralBoundary != 0 || lock.Matched != "" || at < base || at+lock.Len > limit || s.literals[at] || s.model.Store.Tombstones[lock.Word] {
			continue
		}
		e := evidence{start: at, word: []rune(lock.Word), reading: s.syls[at : at+lock.Len]}
		if at > 0 && !s.literals[at-1] {
			e.left = s.syls[at-1]
		}
		if end := at + lock.Len; end < len(s.syls) && !s.literals[end] {
			e.right = s.syls[end]
			for _, g := range s.basePath {
				if g.Start != end || g.End > limit {
					continue
				}
				// 鄰詞只是弱觀察；未成詞的長詞前綴不能憑空當成已接受的詞。
				for _, entry := range s.model.Source().Exact(s.syls[g.Start:g.End]) {
					if entry.Surface == g.Word {
						e.nextWord = g.Word
						e.nextReading = s.syls[g.Start:g.End]
						break
					}
				}
				break
			}
		}
		if len(e.word) == lock.Len && (e.left != "" || e.right != "") {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].start < entries[j].start })
	// 先按目標範圍彙整不同選取，重複相同更正不累加；相衝突的選取各保留一份。
	type span struct {
		start, end int
		left       string
	}
	choices := map[span]map[string]float64{}
	add := func(k span, word string, weight float64) {
		if choices[k] == nil {
			choices[k] = map[string]float64{}
		}
		choices[k][word] = max(choices[k][word], weight)
	}
	for i := base; i < limit; i++ {
		for _, e := range entries {
			n := len(e.reading)
			if i+n > limit || i < e.start+n && e.start < i+n {
				continue
			}
			left := i > base && e.left != "" && s.syls[i-1] == e.left
			right := i+n < limit && e.right != "" && s.syls[i+n] == e.right
			if e.right != "" && i+n < limit && !right {
				continue
			}
			if !left && !right {
				continue
			}
			match := true
			for j, y := range e.reading {
				if s.syls[i+j] != y {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			add(span{i - base, i + n - base, ""}, string(e.word), 1)
			if e.nextWord != "" && i+n+len(e.nextReading) <= limit {
				match := true
				for j, y := range e.nextReading {
					if s.syls[i+n+j] != y {
						match = false
						break
					}
				}
				if match {
					add(span{i + n - base, i + n + len(e.nextReading) - base, string(e.word)}, e.nextWord, .25)
				}
			}
		}
	}
	if len(choices) == 0 {
		return nil
	}
	// 固定排序後預算總量，避免 map 浮點加總順序影響結果。
	totals := map[span]float64{}
	for k, v := range choices {
		var words []string
		for w := range v {
			words = append(words, w)
		}
		sort.Strings(words)
		for _, w := range words {
			totals[k] += v[w]
		}
	}
	return func(start, end int, left, word string) (float64, float64) {
		k := span{start, end, ""}
		if totals[k] == 0 {
			k.left = left
		}
		return choices[k][word], totals[k]
	}
}
