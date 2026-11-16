package personal

import (
	"github.com/0c-biggo/0c-ime/core/emoji"
	"github.com/0c-biggo/0c-ime/core/symbols"
)

// RememberEmoji changes only the bounded palette order. It never learns words,
// pronunciations, English evidence or contextual probabilities.
// 順序依常用＋最近分數（含遲滯）調整，見 emoji.Pick（#124）。
func (m *Model) RememberEmoji(text string) {
	if emoji.Contains(text) {
		m.Store.EmojiRecent, m.Store.EmojiStats = emoji.Pick(m.Store.EmojiRecent, m.Store.EmojiStats, text, m.now())
	}
}

// RememberPaletteTab 記下真的選了東西的 = 選單分頁，下次從這裡開（#124）。
func (m *Model) RememberPaletteTab(tab string) { m.Store.PaletteTab = tab }

// RememberSymbol 記下從 ` 符號選單選的符號，排進「最近」分頁（#290）。同表情：只改選單順序，
// 不學詞、不學讀音；目錄外的文字不記。呼叫端負責私人欄位與暫停學習時不呼叫。
func (m *Model) RememberSymbol(text string) {
	if symbols.Contains(text) {
		m.Store.SymbolRecent, m.Store.SymbolStats = emoji.Rank(symbols.CleanRecent(m.Store.SymbolRecent), m.Store.SymbolStats, text, m.now(), symbols.MaxRecent)
	}
}
