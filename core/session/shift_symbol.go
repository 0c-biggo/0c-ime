package session

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/layout"
)

// shiftedSymbols 是美式鍵盤要按 Shift 才打得出的符號。外殼送來的是按鍵實際產生的字元（Shift＋9 是 "("，
// 見 layout.Layout.Punct），所以核心不必另外知道 Shift 有沒有按著。
const shiftedSymbols = "~!@#$%^&*()_+{}|:\"<>?"

// halfwidthShift（#258，設定「Shift＋符號打半形」）：注音模式下這一鍵打它原本那個全形標點的半形版本（halfwidthPunct），
// 不打全形。只管排列有全形標點的 Shift 符號鍵；@ # $ 等本來就是半形。照舊的有：未按 Shift 的 [ ] ' \（打「」、，
// 給 [ ] 留著唯一一鍵打引號的路）、全形模式（Shift＋空白切的全形，要的就是全形）、英文模式、
// Ctrl＋符號（#168，全形）與微軟參考的反引號前導（全形），後兩者在進到這裡之前就處理掉了。
// 風格本來就打半形原字元的鍵也照風格（#272，微軟參考的 " < > ?，microsoftRawPunctuation）：這個設定是「Shift 組合
// 不打全形」，不是把已經是半形的鍵帽字元換成另一個字。
// 跟沒開時的全形標點一樣：組字中插在游標處、留在組字區。沒在組字時，半形就是鍵帽字元的交還應用程式
// （同 @ #，引擎的 idle_keys 也拿掉它們），不是的（< > "）由核心直接送出半形。
func (s *Session) halfwidthShift(k string) bool {
	if !s.ShiftSymbolHalfwidth || s.fullWidth || s.english || len(k) != 1 || !strings.Contains(shiftedSymbols, k) {
		return false
	}
	if s.InputStyle.Punctuation == "microsoft" && microsoftRawPunctuation(k) {
		return false
	}
	_, ok := s.layout.Punct(rune(k[0]))
	return ok
}

// halfwidthPunct 是全形標點 p 的半形版本：要半形標點的人在中文模式要的是半形的逗號、句號，不是鍵帽上的 < >
// （標準排列不按 Shift 的 , . 是注音鍵，Shift＋, 是注音模式唯一打得出逗號的鍵）。全形英數區（U+FF01–FF5E）
// 換成對應的 ASCII，「。」是 .；沒有對應半形 ASCII 的（『』……——）退回鍵帽字元 k。
func halfwidthPunct(p, k string) string {
	if p == "。" {
		return "."
	}
	if r := []rune(p); len(r) == 1 && r[0] >= 0xFF01 && r[0] <= 0xFF5E {
		return string(r[0] - 0xFEE0)
	}
	return k
}

// halfwidthAlternative：設定開著時，未按 Shift 的標點鍵打出的全形標點按 ↓ 第二項是這一鍵的半形原字元
// （[ → 「 的 ↓ 有 [），跟 Shift 符號打出的半形 ↓ 第二項是全形對稱。設定關閉時不附（#177）。
func (s *Session) halfwidthAlternative(k, p string) bool {
	return s.ShiftSymbolHalfwidth && !s.fullWidth && len(k) == 1 && !strings.Contains(shiftedSymbols, k) && utf8.RuneCountInString(p) == 1
}

// insertHalfwidthShift 在游標處插入 Shift 符號鍵 k 的半形；↓ 第二項是這一鍵原本打的全形標點 p，
// 鍵帽字元跟半形不同時（< > "）排在後面，< > 仍打得到。同組符號已經列了的不重複（& 的「＆」、^ 的「……」）。
func (s *Session) insertHalfwidthShift(k, p string) {
	half := halfwidthPunct(p, k)
	s.insertLiteral(half)
	known := symbolAlternatives(half)
	var alts []string
	for _, a := range []string{p, k} {
		if !slices.Contains(known, a) && !slices.Contains(alts, a) {
			alts = append(alts, a)
		}
	}
	if len(alts) > 0 {
		s.setLiteralKey(s.cursor-1, strings.Join(alts, literalKeySep))
	}
}

// HalfwidthShiftKeys 回報設定「Shift＋符號打半形」開啟時，排列 l 裡改打半形、沒在組字時交還應用程式的按鍵（#258）：
// 半形就是鍵帽字元的那些。引擎從 IdleKeys 拿掉它們，Windows 的 TestKeyDown 才不會吃掉之後又沒處理；
// < > " 打的是 , . ;，由核心送出，照舊留在 IdleKeys。
func HalfwidthShiftKeys(l layout.Layout) string {
	if l == nil {
		l = layout.Standard
	}
	var b strings.Builder
	for _, r := range shiftedSymbols {
		if p, ok := l.Punct(r); ok && halfwidthPunct(p, string(r)) == string(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
