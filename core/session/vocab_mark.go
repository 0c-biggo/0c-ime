package session

import (
	"fmt"
	"strings"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 組字中加詞與反悔鍵（#123，Preview02 打開才處理；0.2.0 起宿主一律打開）：
//   - Shift＋←／→ 從游標標出一段範圍，Enter 把畫面上這段字連同使用者打的注音加入自訂詞彙，Esc 取消；
//     其他按鍵取消標示後照常處理。
//   - 候選窗開著時按反悔鍵（Shift＋Delete，macOS 也可以 Shift＋⌫）：反白的候選是自訂詞就刪除
//     （記成一個批次，管理頁可以復原），是學到的詞就忘記（同 Forget）。
// 開關關閉時這些鍵一律回 handled=false，外殼原樣交還應用程式；外殼也只在引擎回報支援時才送新鍵名。

// vocabBaseKey 是新鍵名在沒有對應功能時的原本按鍵。外殼只要「按著 Shift、引擎回報 vocab_keys」就送新鍵名，
// 不必追蹤候選窗狀態：開關關閉、候選窗沒開時的反悔鍵、候選窗開著時的 Shift＋←／→，都照原本的鍵處理，
// 行為與沒有這個功能時相同（不會把 Shift＋⌫ 漏給應用程式刪到組字區外的字）。
var vocabBaseKey = map[string]string{
	proto.KeyShiftLeft: proto.KeyLeft, proto.KeyShiftRight: proto.KeyRight,
	proto.KeyShiftDelete: proto.KeyDelete, proto.KeyShiftBackspace: proto.KeyBackspace,
}

func (s *Session) normalizeVocabKey(k string) string {
	base, ok := vocabBaseKey[k]
	if !ok {
		return k
	}
	switch {
	case !s.Preview02:
		return base
	case k == proto.KeyShiftDelete || k == proto.KeyShiftBackspace:
		if s.cand == nil || s.cand.kind != "" || s.cand.english || s.cand.mixed {
			return base
		}
	case s.cand != nil:
		return base
	}
	return k
}

// markState 是組字中標示的範圍：另一端是游標。
type markState struct{ anchor int }

func (s *Session) markRange() (start, end int) {
	if s.mark == nil {
		return 0, 0
	}
	return min(s.mark.anchor, s.cursor), max(s.mark.anchor, s.cursor)
}

// vocabKey 處理組字中加詞的按鍵；consumed 為 false 時呼叫端照一般規則處理這個鍵。
func (s *Session) vocabKey(k string) (handled, consumed bool) {
	switch k {
	case proto.KeyShiftLeft, proto.KeyShiftRight:
		if !s.composing() {
			return false, true // 沒在組字：交還應用程式（照常選取文字）
		}
		if !s.comp.Empty() || s.mixed.raw != "" || s.english {
			return true, true // 注音或英文打到一半：先打完再標示
		}
		if s.mark == nil {
			s.mark = &markState{anchor: s.cursor}
		}
		step := 1
		if k == proto.KeyShiftLeft {
			step = -1
		}
		next := s.literalStep(s.cursor, step)
		if lo, hi := min(s.mark.anchor, next), max(s.mark.anchor, next); hi-lo > vocab.MaxWordRunes {
			s.notices = append(s.notices, fmt.Sprintf("最多選 %d 個字", vocab.MaxWordRunes))
			return true, true
		}
		s.cursor = next
		s.cand = nil
		return true, true
	}
	if s.mark == nil {
		return false, false
	}
	// 結束標示時游標回到開始標示的位置（新酷音、小麥注音會停在範圍另一端，接著打的字插錯地方）。
	anchor := s.mark.anchor
	switch k {
	case proto.KeyEnter:
		s.addMarked()
		s.mark, s.cursor = nil, anchor
		return true, true
	case proto.KeyEscape:
		s.mark, s.cursor = nil, anchor
		return true, true
	case proto.KeyBackspace, proto.KeyDelete:
		// 跟一般文字框一樣刪掉選取的字（小麥注音會刪到沒選的字）。
		start, end := s.markRange()
		s.mark = nil
		s.cursor = end
		for s.cursor > start {
			s.eraseBeforeCursor()
		}
		return true, true
	}
	s.mark = nil // 其他按鍵：取消標示，照常處理
	return false, false
}

// minComposeWord：組字中加詞至少兩個字（同小麥注音、威注音）。單字讀音在設定程式加，避免誤加單字污染選字。
const minComposeWord = 2

// markHint 是標示中顯示的說明（四個外殼都以提示顯示）：按 Enter 會發生什麼，附讀音；不能加入時說明原因。
func (s *Session) markHint() string {
	start, end := s.markRange()
	if start == end {
		return ""
	}
	if end-start < minComposeWord {
		return "再多選一個字（單字的讀音請到設定程式「我的詞彙」加入）"
	}
	for i := start; i < end; i++ {
		if s.literals[i] {
			return "範圍裡有英文或符號，不能加入詞庫；這類文字可以放「我的片語」"
		}
	}
	word := s.textIn(start, end)
	if len([]rune(word)) != end-start || strings.ContainsFunc(word, func(r rune) bool { return zhuyin.ClassOf(r) != zhuyin.None }) {
		return "這段還沒選好字，先選字再加入"
	}
	syls, ok := s.typedReading(start, end)
	if !ok {
		return "這段有沒打聲調、還認不出讀音的字，補上聲調再加入"
	}
	reading := zhuyin.Key(syls)
	switch {
	case s.model.VocabReading(word, syls):
		return "「" + word + "」已在詞庫　Esc 取消"
	case s.systemHas(word, syls):
		return "Enter 設為優先：" + word + "（" + reading + "）　Esc 取消"
	}
	return "Enter 加入詞庫：" + word + "（" + reading + "）　Esc 取消"
}

// systemHas：系統詞庫有這個詞、這個讀音。
func (s *Session) systemHas(word string, syls []zhuyin.Syllable) bool {
	for _, e := range s.model.Lex.Exact(syls) {
		if e.Surface == word && !e.Correction {
			return true
		}
	}
	return false
}

// addMarked 把標示的範圍加入自訂詞彙。讀音用使用者打的注音，破音字不會猜錯。
func (s *Session) addMarked() {
	start, end := s.markRange()
	if start == end {
		return
	}
	if end-start < minComposeWord {
		s.notices = append(s.notices, "至少選兩個字；單字的讀音請到設定程式「我的詞彙」加入")
		return
	}
	if s.private || s.paused || s.localBlocked {
		s.notices = append(s.notices, "這裡不加入詞庫")
		return
	}
	for i := start; i < end; i++ {
		if s.literals[i] {
			s.notices = append(s.notices, "只能把注音打出的字加入詞庫")
			return
		}
	}
	word := s.textIn(start, end)
	if len([]rune(word)) != end-start || strings.ContainsFunc(word, func(r rune) bool { return zhuyin.ClassOf(r) != zhuyin.None }) {
		s.notices = append(s.notices, "這段還沒有選好字，先選字再加入")
		return
	}
	syls, ok := s.typedReading(start, end) // 沒打聲調的字用解碼走的詞庫讀音（#150）
	if !ok {
		s.notices = append(s.notices, "這段有沒打聲調、還認不出讀音的字，補上聲調再加入")
		return
	}
	system := s.systemHas(word, syls)
	b, err := s.model.VocabAdd([]vocab.Entry{{Text: word, Readings: []string{zhuyin.Key(syls)}}}, vocab.SourceCompose)
	if err != nil {
		s.notices = append(s.notices, err.Error())
		return
	}
	if b == nil {
		s.notices = append(s.notices, "「"+word+"」已在詞庫")
		return
	}
	s.vocabChanged = true
	if s.locks == nil {
		s.locks = map[int]decode.Lock{}
	}
	for at := range s.locks { // 範圍內原本的鎖定由整個詞取代，畫面不變
		if start <= at && at < end {
			delete(s.locks, at)
		}
	}
	s.locks[start] = decode.Lock{Word: word, Len: end - start}
	if system {
		s.notices = append(s.notices, "已設為優先："+word)
	} else {
		s.notices = append(s.notices, "已加入詞庫："+word)
	}
}

// textIn 回傳音節 [start, end) 目前顯示的字。
func (s *Session) textIn(start, end int) string {
	var text strings.Builder
	if len(s.path) == 0 {
		for i := start; i < end; i++ {
			displayed, _ := s.fallbackDisplay(i, s.syls[i])
			text.WriteString(displayed)
		}
		return text.String()
	}
	for _, g := range s.path {
		if g.End <= start || g.Start >= end {
			continue
		}
		runes := []rune(g.Word)
		if len(runes) != g.End-g.Start { // 尾端前綴等字數與音節數不同的分段：取不出對應的字
			return ""
		}
		text.WriteString(string(runes[max(0, start-g.Start):min(len(runes), end-g.Start)]))
	}
	return text.String()
}

// markSegments 把組字區 [start, end)（字元位置）的分段改成標示樣式。
func markSegments(segs []proto.Segment, start, end int) []proto.Segment {
	var out []proto.Segment
	at := 0
	for _, g := range segs {
		runes := []rune(g.Text)
		lo, hi := at, at+len(runes)
		at = hi
		cut := func(a, b int, style string) {
			if a < b {
				out = append(out, proto.Segment{Text: string(runes[a-lo : b-lo]), Style: style})
			}
		}
		if hi <= start || lo >= end {
			out = append(out, g)
			continue
		}
		cut(lo, max(lo, start), g.Style)
		cut(max(lo, start), min(hi, end), proto.StyleMarked)
		cut(min(hi, end), hi, g.Style)
	}
	return out
}

// forgetCandidate 是候選窗的反悔鍵：反白的候選是自訂詞就刪除（可在管理頁復原），
// 是學到的詞就忘記；系統詞庫本來就有、也沒學過的詞不動。
func (s *Session) forgetCandidate() {
	c := s.cand
	if c == nil || c.kind != "" || c.english || c.mixed || c.cursor < 0 || c.cursor >= len(c.items) {
		return
	}
	word := c.items[c.cursor].Entry.Surface
	switch {
	case s.model.IsVocab(word):
		if _, err := s.model.VocabRemove([]string{word}, vocab.SourceCompose); err != nil {
			s.notices = append(s.notices, err.Error())
			return
		}
		s.vocabChanged = true
		s.notices = append(s.notices, "已從詞庫刪除："+word+"（可在設定程式復原）")
	case s.model.Learned(word):
		s.model.Forget(word)
		s.forgot = true
		s.notices = append(s.notices, "不再推薦："+word)
	default:
		s.notices = append(s.notices, "「"+word+"」是內建的詞，沒有可以刪除的學習")
		return
	}
	// 被刪的詞可能正鎖定在組字裡：解除鎖定，重新解碼。
	for at, l := range s.locks {
		if l.Word == word {
			delete(s.locks, at)
		}
	}
	anchor, length, before, cursor := c.start, c.length, c.before, c.cursor
	if before {
		anchor += length
	}
	s.cand = nil
	s.refresh()
	// 這個詞長沒有別的原音候選了（只剩近音）：照 ↓ 重新開。
	if !s.openAnchored(anchor, length, before) || !s.cand.hasExact() {
		s.cand = nil
		s.openCandidates(0)
	} else {
		s.cand.cursor = min(cursor, len(s.cand.items)-1) // 原地重排，反白停在同一個位置
	}
}
