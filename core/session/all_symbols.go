package session

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/layout"
)

// allSymbolsKey（#282，設定「標點寬度：所有符號鍵打半形」）：注音模式半形時，排列有全形標點的鍵不論有沒有按 Shift，
// 都打鍵帽上的半形字元（Shift＋- 是 _、Shift＋, 是 <、[ 是 [）。
//   - 沒在組字時交還應用程式（同 @ #；引擎的 idle_keys 也拿掉這些鍵）。
//   - 打到一半的中英混打英文收下識別字、網址、路徑裡的符號（user_name、search?q=1、C:\Users、don't），
//     括號與其他符號讓英文先定案（DNS(我)）。
//   - 組字中插在游標處、留在組字區，↓ 第二項是這一鍵原本的全形標點；未打完的注音同其他標點留成符號。
//
// 全形標點用 Ctrl＋符號（#168，宿主在這個設定開著時固定打開 CtrlPunct）。照舊的有：英文模式、全形（Shift＋空白切的字寬）、
// 候選窗開著時、微軟參考的反引號前導。設定排在輸入習慣的標點規則之前：選了這個值，所有風格的符號鍵都打半形。
func (s *Session) allSymbolsKey(k string) (handled bool, commit string, done bool) {
	if !s.AllSymbolsHalfwidth || s.english || s.fullWidth || s.symbolPrefix || s.cand != nil || len(k) != 1 {
		return false, "", false
	}
	p, ok := s.layout.Punct(rune(k[0]))
	if !ok {
		return false, "", false
	}
	if strings.ContainsAny(k, ":\\'_^?&") && s.mixedKeepsPunctuation(k) {
		return true, "", true
	}
	if !s.composing() {
		return false, "", true
	}
	s.flushMixed()
	s.insertPendingLiteral()
	s.insertHalfwidth(k, k, p)
	return true, "", true
}

// AllSymbolsHalfwidthKeys 回報「所有符號鍵打半形」（#282）時，排列 l 裡沒在組字時交還應用程式的按鍵：排列有全形標點的鍵都是。
// 引擎從 IdleKeys 拿掉它們，Windows 的 TestKeyDown 才不會吃掉之後又沒處理。
func AllSymbolsHalfwidthKeys(l layout.Layout) string {
	if l == nil {
		l = layout.Standard
	}
	var b strings.Builder
	for r := rune(0x21); r <= 0x7e; r++ {
		if _, ok := l.Punct(r); ok {
			b.WriteRune(r)
		}
	}
	return b.String()
}
