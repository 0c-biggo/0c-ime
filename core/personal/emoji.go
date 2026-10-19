package personal

import "git.biggo.com/0c/ime/core/emoji"

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
