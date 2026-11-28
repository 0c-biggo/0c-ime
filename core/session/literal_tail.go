package session

import (
	"maps"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 把組字尾端換成字面文字（literal 請求，APP#33）：手機的中英混打在候選列列出注音鍵照字母位置拼出的英文字
// （ㄩㄍㄍㄔㄛㄙㄕ 是 meeting），點了要把那幾鍵換成這個字留在組字裡、接著打中文。原本外殼要清空組字、照原本的鍵
// 重打一次再逐字母送 key，長句在手機上要將近一秒；這裡直接拿掉尾端那幾個位置（連同未打完的注音），在原處放進
// 字面文字。哪幾個位置是那幾鍵由外殼決定（它知道每一鍵），核心不核對讀音，也不看是不是英文。
//
// 放進去的字跟中英混打照打的英文一樣（mixedOrigins 同 insertMixedLiteral，不是使用者另外確認過的寫法），學習照舊。
// 換掉的音節與未打完的注音記在 tailUndo：游標在這段文字後面、它還是組字的結尾時，退格把它換回原本的注音，
// 跟點完緊接著退格撤銷這次點選一樣；之後接著打的字刪回來，再退格也一樣還原。一句裡換了兩個字時，刪回前一個字
// 也一樣。送出、取消（reset）時丟掉。

// tailLiteralMax 是一次放進的字面文字上限（位元組），同英文字典的詞長上限。
const tailLiteralMax = english.MaxWordBytes

// tailUndoMax 是一段組字最多記得幾次換進去的文字（一句裡的英文字）；更早的退格時照一般的刪。
const tailUndoMax = 16

// tailLiteral 是 literal 請求換掉的東西：start 起的字面文字 word，換掉之前的組字（saved，只用 start 以後的位置）
// 與未打完的注音。
type tailLiteral struct {
	start         int
	word          []rune
	saved         syllableState
	comp          zhuyin.Composer
	compNear      map[zhuyin.Class]spatialSlot
	symbolPending bool
}

// literalTail 把游標前最後 length 個打的音節連同未打完的注音換成 word。游標要在組字結尾，不能有九宮格、拼音或中英混打
// 的暫存按鍵；word 不能有控制字元或空白以外的分隔字元。換不了時回傳 false，組字不變。
func (s *Session) literalTail(length int, word string) bool {
	if !tailText(word) || length < 0 || s.cursor != len(s.syls) || length == 0 && s.comp.Empty() ||
		s.mixed.raw != "" || s.keysPending() || s.symbolPrefix || s.english {
		return false
	}
	start, ok := s.tailStart(length)
	if !ok {
		return false
	}
	undo := &tailLiteral{start: start, word: []rune(word), saved: s.cloneSyllables(), comp: s.comp,
		compNear: maps.Clone(s.compNear), symbolPending: s.symbolPending}
	s.cand = nil
	s.comp.Reset()
	s.compNear, s.pendingNear, s.symbolPending = nil, nil, false
	s.pendingSpatial, s.pendingKeys, s.nineStaged = nil, nil, nil
	for s.cursor > start {
		s.eraseBeforeCursor()
	}
	// 同 insertMixedLiteral，但不先解碼：那是為了找插入點切開的詞，組字結尾不會切開詞，省下長句（尤其只打首音）
	// 前段的一次整段解碼。
	for _, r := range word {
		at := s.cursor
		s.insert(zhuyin.Syllable(string(r)))
		if s.literals == nil {
			s.literals = map[int]bool{}
		}
		s.literals[at] = true
		if s.mixedOrigins == nil {
			s.mixedOrigins = map[int]mixedOrigin{}
		}
		s.mixedOrigins[at] = mixedOrigin{}
	}
	s.tailUndo = append(s.tailUndo, undo)
	if len(s.tailUndo) > tailUndoMax {
		s.tailUndo = slices.Delete(s.tailUndo, 0, len(s.tailUndo)-tailUndoMax)
	}
	return true
}

// tailStart 是最後 length 個打的音節從哪個位置開始，跟外殼照打的鍵數音節的算法相同：合併弧合成的位置照打的算
// （TypedAt），原樣留下的注音（連同聲調符號，佔好幾個位置）算一個，其他字面文字一個字一個。切在合成的位置中間時
// 回傳 false。
func (s *Session) tailStart(length int) (int, bool) {
	at := s.cursor
	for n := length; n > 0; n-- {
		if at == 0 {
			return 0, false
		}
		if p, ok := s.pendingLiteralEnding(at); ok {
			at = p.start
			continue
		}
		k := max(1, len(s.TypedAt(at-1)))
		if k > n {
			return 0, false
		}
		n -= k - 1
		at--
	}
	return at, true
}

// pendingLiteralEnding 是剛好在 end 結束（含聲調符號）的原樣注音。
func (s *Session) pendingLiteralEnding(end int) (pendingLiteral, bool) {
	for _, p := range s.pendingLiterals {
		if p.end()+p.mark() == end && p.start < end {
			return p, true
		}
	}
	return pendingLiteral{}, false
}

func tailText(word string) bool {
	if word == "" || len(word) > tailLiteralMax || !utf8.ValidString(word) {
		return false
	}
	for _, r := range word {
		if unicode.IsControl(r) || unicode.IsSpace(r) && r != ' ' {
			return false
		}
	}
	return true
}

// undoLiteralTail 是 literal 請求之後的退格：游標前就是那段文字、它還是組字的結尾時，換回原本的音節與未打完的注音。
// 換掉之後前面的組字可能改過（選了詞），那些照現在的；只有那段文字的位置換回原本的。
func (s *Session) undoLiteralTail() bool {
	// 從最後換的找起：已經不在原處（刪掉了、前面插了字）的丟掉；還在、但後面接著別的字時先留著，刪回來再還原。
	for len(s.tailUndo) > 0 && !s.tailUndo[len(s.tailUndo)-1].present(s) {
		s.tailUndo = s.tailUndo[:len(s.tailUndo)-1]
	}
	if len(s.tailUndo) == 0 {
		return false
	}
	u := s.tailUndo[len(s.tailUndo)-1]
	end := u.start + len(u.word)
	if s.cursor != end || len(s.syls) != end || !s.comp.Empty() || s.mixed.raw != "" || s.keysPending() {
		return false
	}
	s.tailUndo = s.tailUndo[:len(s.tailUndo)-1]
	for s.cursor > u.start {
		s.eraseBeforeCursor()
	}
	s.restoreTail(u.start, u.saved)
	s.comp, s.compNear, s.symbolPending = u.comp, u.compNear, u.symbolPending
	s.pathSig = ""
	return true
}

// present 回報換進去的文字還在原處。
func (u *tailLiteral) present(s *Session) bool {
	if u.start+len(u.word) > len(s.syls) {
		return false
	}
	for i, r := range u.word {
		if !s.literals[u.start+i] || s.syls[u.start+i] != zhuyin.Syllable(string(r)) {
			return false
		}
	}
	return true
}

// cloneSyllables 是 saveSyllables 的深複本：tailUndo 要留到之後的請求，組字裡有些表會原地改寫。
func (s *Session) cloneSyllables() syllableState {
	st := s.saveSyllables()
	st.syls = slices.Clone(st.syls)
	st.locks, st.literals, st.mixedKeys, st.literalKeys = maps.Clone(st.locks), maps.Clone(st.literals), maps.Clone(st.mixedKeys), maps.Clone(st.literalKeys)
	st.mixedOrigins, st.displayed = maps.Clone(st.mixedOrigins), maps.Clone(st.displayed)
	st.pendingLiterals = slices.Clone(st.pendingLiterals)
	st.spatial, st.nine, st.nineSeg, st.merges, st.keyNear = maps.Clone(st.spatial), maps.Clone(st.nine), maps.Clone(st.nineSeg), maps.Clone(st.merges), maps.Clone(st.keyNear)
	return st
}

// restoreTail 把 saved 從 start 起的位置接回組字結尾（組字現在剛好 start 個位置）。
func (s *Session) restoreTail(start int, saved syllableState) {
	s.syls = append(s.syls[:start:start], saved.syls[start:]...)
	s.cursor = len(s.syls)
	s.locks = tailMerge(s.locks, saved.locks, start)
	if s.locks == nil {
		s.locks = map[int]decode.Lock{}
	}
	s.literals = tailMerge(s.literals, saved.literals, start)
	s.mixedKeys = tailMerge(s.mixedKeys, saved.mixedKeys, start)
	s.literalKeys = tailMerge(s.literalKeys, saved.literalKeys, start)
	s.mixedOrigins = tailMerge(s.mixedOrigins, saved.mixedOrigins, start)
	s.displayed = tailMerge(s.displayed, saved.displayed, start)
	s.spatial = tailMerge(s.spatial, saved.spatial, start)
	s.nine = tailMerge(s.nine, saved.nine, start)
	s.nineSeg = tailMerge(s.nineSeg, saved.nineSeg, start)
	s.merges = tailMerge(s.merges, saved.merges, start)
	s.keyNear = tailMerge(s.keyNear, saved.keyNear, start)
	pending := slices.Clone(s.pendingLiterals)
	for _, p := range saved.pendingLiterals {
		if p.start >= start {
			pending = append(pending, p)
		}
	}
	s.pendingLiterals = pending
	rejected := slices.Clone(s.rejected)
	for _, r := range saved.rejected {
		if r.Start >= start {
			rejected = append(rejected, r)
		}
	}
	s.rejected = slices.Clip(rejected)
}

// tailMerge 是 now 在 start 以前的項目加上 saved 在 start 以後的項目（新的表）；兩邊都沒有時是 nil。
func tailMerge[V any](now, saved map[int]V, start int) map[int]V {
	var out map[int]V
	put := func(k int, v V) {
		if out == nil {
			out = map[int]V{}
		}
		out[k] = v
	}
	for k, v := range now {
		if k < start {
			put(k, v)
		}
	}
	for k, v := range saved {
		if k >= start {
			put(k, v)
		}
	}
	return out
}
