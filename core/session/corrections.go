package session

import (
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// configureCorrections 的位置與當前字面符號分段一致。只讀已提交的偏好；
// 當次鎖定以暫時覆蓋處理，取消、改選、刪除及重播都不寫入持久記錄。
// 跨音手選的讀音不是使用者打的音，不當作更正偏好。
func (s *Session) configureCorrections(begin, end int, local bool) {
	var selected []personal.Seg
	if local && !s.private && !s.paused && !s.localBlocked {
		// 以位置排序，避免 map 順序影響同範圍覆蓋。
		for at := begin; at < end; at++ {
			if lock, ok := s.locks[at]; ok && lock.LiteralBoundary == 0 && lock.Matched == "" && at+lock.Len <= end && !s.literals[at] {
				selected = append(selected, personal.Seg{Word: lock.Word, Start: at - begin, End: at + lock.Len - begin, Manual: true, Completion: lock.Completion})
			}
		}
	}
	syllables := s.syls[begin:end]
	s.dec.CorrectionPenalty = s.model.CorrectionPenalty(syllables, selected)
}

// enclosedSelections 找出被較長的詞包住、所以在路徑上沒有自己一段的手選（解碼器允許詞完整包住鎖定，
// 例：在「為甚麼」裡選「什麼」顯示成「為什麼」）。提交時路徑上只有「為什麼」，手選本身若不另外交給
// 學習，就不會留下更正或個人讀音，下次照樣顯示「甚麼」（#96）。跨音手選與字面邊界的暫時鎖定不算。
// syls 是提交時的讀音（resolvedSyllables）：跟路徑的段一樣，碰到跨音或沒能換回詞庫讀音的沒打聲調音節（#150）
// 就標成 Fuzzy，不學。
func (s *Session) enclosedSelections(syls []zhuyin.Syllable) []personal.Seg {
	var out []personal.Seg
	for at := range len(s.syls) {
		lock, ok := s.locks[at]
		if !ok || lock.LiteralBoundary != 0 || lock.Matched != "" || s.literals[at] {
			continue
		}
		for _, g := range s.path {
			if g.Start <= at && at+lock.Len <= g.End && (g.Start != at || g.End != at+lock.Len) && !g.Partial && !s.literals[g.Start] {
				out = append(out, personal.Seg{Word: lock.Word, Start: at, End: at + lock.Len, Manual: true,
					Completion: lock.Completion, Suggested: lock.Suggested, Fuzzy: s.fuzzyTouches(at, at+lock.Len) || untonedTouches(syls, at, at+lock.Len)})
				break
			}
		}
	}
	return out
}
