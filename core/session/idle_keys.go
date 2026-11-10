package session

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// IdleKeys 回報沒在組字、在注音模式時輸入法會處理的可見 ASCII 按鍵（0x21–0x7e，不含空白）：
// 注音鍵、標點、` 符號選單、= Emoji 選單，以及沒開混打時的聲調鍵（單按＋空白是聲調符號，#135）；中英混打時照混打規則。
// 其餘按鍵（不是注音鍵的數字、Shift＋字母、@ 等）回 handled=false 交還應用程式。
//
// 給自己先決定閒置按鍵要不要交給核心的外殼（Windows 的 TestKeyDown）：答案依鍵盤排列與設定而不同（#183 許氏的
// 數字、- / 交還應用程式），由引擎回報，外殼不必各自維護每一種排列的按鍵表。session 的測試逐鍵比對實際的處理結果。
// 只看設定不看個別輸入框：敏感欄位不開 Emoji 選單，那時 = 照舊交還應用程式（外殼以前也是只看設定）。
func IdleKeys(l layout.Layout, mixedInput bool, emojiShortcut string) string {
	if l == nil {
		l = layout.Standard
	}
	mixed := mixedInputActive(mixedInput, l == layout.Standard)
	_, equalsPhonetic := l.Symbol('=')
	var b strings.Builder
	for r := rune(0x21); r <= 0x7e; r++ {
		sym, phonetic := l.Symbol(r)
		_, punct := l.Punct(r)
		switch {
		case r == '`', punct:
		case r == '=' && emojiShortcut == "equals" && !equalsPhonetic:
		case mixed:
		case phonetic && (zhuyin.ClassOf(sym) != zhuyin.Tone || sym != zhuyin.Tone1):
		default:
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FullWidthIdleKeys 回報全形（Shift＋空白切的字寬，#278）時沒在組字、注音模式會處理的可見 ASCII 按鍵。
// 全形時不是注音鍵也不是標點的鍵（@ # $ * + 數字，許氏的數字與 q）與 Shift＋字母打成全形字元（styleKey），
// 注音鍵與標點照半形時的規則（IdleKeys）。「Shift＋符號打半形」與微軟參考風格的半形原字元在全形時不適用，
// 所以從沒拿掉這些鍵的 IdleKeys 算起。數字鍵盤全形時一律打成全形字元，不在這份清單（外殼看字寬自己判斷）。
// 字寬是 session 的狀態：外殼依快照的 full_width 在這份與 IdleKeys 之間選。
func FullWidthIdleKeys(l layout.Layout, mixedInput bool, emojiShortcut string) string {
	if l == nil {
		l = layout.Standard
	}
	idle := IdleKeys(l, mixedInput, emojiShortcut)
	var b strings.Builder
	for r := rune(0x21); r <= 0x7e; r++ {
		_, phonetic := l.Symbol(r)
		_, punct := l.Punct(r)
		if r >= 'A' && r <= 'Z' || !phonetic && !punct || strings.ContainsRune(idle, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
