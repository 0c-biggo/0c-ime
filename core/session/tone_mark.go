package session

import (
	"slices"
	"strings"
	"unicode"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聲調符號（#135）：沒開中英混打時，注音暫存是空的、單按聲調鍵（標準排列 3 4 6 7、倚天 1 2 3 4）
// 再按空白，輸出這個聲調符號 ˇ ˋ ˊ ˙。
//
// 新酷音（libchewing zhuyin_layout/standard.rs、et.rs）讓注音暫存只放一個聲調，空白（一聲）完成後
// 以「只有聲調」的讀音查詞庫，詞庫裡正好有 ˇ ˊ ˋ ˙ 四個詞（libchewing-data tsi.csv）。新酷音接著打注音時
// 聲調會被丟掉、連打 34 會變成 ˋ；這裡只取「單獨的聲調鍵緊接空白」：按下聲調鍵時照舊顯示數字（Enter、
// 注音、其他按鍵的結果與以前相同），緊接著的空白才換成聲調符號；前面緊接著數字的聲調鍵是數字的一部分。
// 中英混打的數字＋空白另有規則（#78、#134、#162），不在這裡處理。
//
// 沒在組字時聲調鍵也要先留在組字區（以前直接交給應用程式），否則空白來時數字已經送出去了。

type toneKeyPending struct {
	at  int    // 組字區裡這個數字的位置
	key string // 原本的按鍵
}

// ToneMarkKeys 回報這個設定下，沒在組字時的聲調鍵是否由輸入法處理（先留在組字區等空白）。
// 外殼若自己判斷閒置按鍵要不要交給核心（Windows 的 TestKeyDown），依這個答案決定聲調鍵。
func ToneMarkKeys(mixedInput, standardLayout bool) bool {
	return !mixedInputActive(mixedInput, standardLayout)
}

// toneKey 處理注音暫存是空的時按下的聲調鍵（空白的一聲不算）：數字照字面留在游標處；
// 前面沒有緊接著數字時記下來，下一鍵是空白就換成聲調符號（finishToneKey）。
func (s *Session) toneKey(k string, sym rune) bool {
	if s.mixedInput() || s.english || !s.comp.Empty() || zhuyin.ClassOf(sym) != zhuyin.Tone || sym == zhuyin.Tone1 {
		return false
	}
	lone := s.literalNumberBefore() == ""
	s.insertLiteral(k)
	if lone {
		s.pendingToneKey = &toneKeyPending{at: s.cursor - 1, key: k}
	}
	return true
}

// finishToneKey 把緊接在空白之前的單獨聲調鍵換成聲調符號；原按鍵記在 literalKeys，↓ 可選回數字。
func (s *Session) finishToneKey(p *toneKeyPending) bool {
	if p == nil || !s.comp.Empty() || s.cursor != p.at+1 || !s.literals[p.at] || string(s.syls[p.at]) != p.key {
		return false
	}
	sym, ok := s.layout.Symbol([]rune(p.key)[0])
	if !ok || zhuyin.ClassOf(sym) != zhuyin.Tone || sym == zhuyin.Tone1 {
		return false
	}
	s.eraseBeforeCursor()
	s.insertLiteral(string(sym))
	s.setLiteralKey(p.at, p.key)
	return true
}

// setLiteralKey 記下照字面留下的符號是哪個按鍵打出的，↓ 第二項可選回；編輯時隨位置平移，被換掉或刪除時一起消失。
func (s *Session) setLiteralKey(at int, key string) {
	if s.literalKeys == nil {
		s.literalKeys = map[int]string{}
	}
	s.literalKeys[at] = key
}

// literalKeyAlternatives：聲調鍵打出的聲調符號、混打的 Shift＋, Shift＋. 打出的「，」「。」（#190）按 ↓ 時，
// 原按鍵排第二個（同 #134 單鍵出字的原按鍵）。反過來，落單的聲調鍵還是數字時（中英混打一律如此，#153；
// 沒開混打時是還沒按空白），↓ 第一個仍是數字，第二個給這個鍵的聲調符號。
func (s *Session) literalKeyAlternatives(items []string, start, length int) []string {
	if length != 1 || len(items) == 0 {
		return items
	}
	if k := s.literalKeys[start]; k != "" {
		return slices.Insert(items, 1, k)
	}
	if sym, ok := s.loneToneKeyAt(start); ok {
		return slices.Insert(items, 1, string(sym))
	}
	if sym, ok := s.lonePhoneticKeyAt(start); ok {
		return slices.Insert(items, 1, string(sym))
	}
	return items
}

// lonePhoneticKeyAt（#208）：中英混打照字面留著的單一注音鍵（「我2 」的 2、句首的 1 - ,），前後沒有緊接著英數或數字標點時，
// 回傳它在目前排列的注音符號，↓ 第二項給它（2 → ㄉ），比照 loneToneKeyAt 的聲調鍵。混打下單獨打 1 2 , 接空白照舊是數字與標點（#162），
// 要注音文（我ㄉ天）時打完按 ↓ 選，或像這樣回頭在字上按 ↓。沒開混打時組字區裡的字面數字來自數字鍵盤，不加。
func (s *Session) lonePhoneticKeyAt(at int) (rune, bool) {
	if !s.mixedInput() || at < 0 || at >= len(s.syls) || !s.literals[at] {
		return 0, false
	}
	rs := []rune(string(s.syls[at]))
	if len(rs) != 1 || rs[0] > unicode.MaxASCII {
		return 0, false
	}
	sym, ok := s.layout.Symbol(rs[0])
	if !ok || zhuyin.ClassOf(sym) == zhuyin.Tone || zhuyin.ClassOf(sym) == zhuyin.None {
		return 0, false
	}
	if s.alnumLiteralAt(at-1) || s.alnumLiteralAt(at+1) {
		return 0, false
	}
	return sym, true
}

// loneToneKeyAt：at 是照字面留著的聲調鍵數字（不含一聲的空白），前後沒有緊接著數字、英文或數字標點，
// 也就是不屬於「36」「x6」「3.6」這類英數。回傳這個鍵在目前排列的聲調符號。
func (s *Session) loneToneKeyAt(at int) (rune, bool) {
	if at < 0 || at >= len(s.syls) || !s.literals[at] {
		return 0, false
	}
	rs := []rune(string(s.syls[at]))
	if len(rs) != 1 || !unicode.IsDigit(rs[0]) || rs[0] > unicode.MaxASCII {
		return 0, false
	}
	sym, ok := s.layout.Symbol(rs[0])
	if !ok || zhuyin.ClassOf(sym) != zhuyin.Tone || sym == zhuyin.Tone1 {
		return 0, false
	}
	if s.alnumLiteralAt(at-1) || s.alnumLiteralAt(at+1) {
		return 0, false
	}
	return sym, true
}

// alnumLiteralAt：pos 是照字面留著的 ASCII 英數或數字標點（. , : / -）。
func (s *Session) alnumLiteralAt(pos int) bool {
	if pos < 0 || pos >= len(s.syls) || !s.literals[pos] {
		return false
	}
	r := []rune(string(s.syls[pos]))
	return len(r) == 1 && r[0] <= unicode.MaxASCII && (unicode.IsLetter(r[0]) || unicode.IsDigit(r[0]) || strings.ContainsRune(".,:/-", r[0]))
}

// shiftKeys 平移位置→原按鍵的紀錄：插入時其後的右移，刪除時該格移除、其後左移。
func shiftKeys(keys map[int]string, at, delta int) map[int]string {
	if len(keys) == 0 {
		return keys
	}
	next := map[int]string{}
	for pos, k := range keys {
		switch {
		case pos < at:
			next[pos] = k
		case delta > 0 || pos > at:
			next[pos+delta] = k
		}
	}
	return next
}
