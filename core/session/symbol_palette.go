package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/symbols"
)

// 反引號（`）開的符號選單分頁（#290）：「最近」＋ symbols.Tabs，Tab／Shift＋Tab 換分頁，比照 = 選單。
// 選單沿用一般候選的版面與翻頁（cand.kind 是空字串），只多記目前的分頁 cand.symbols。

// symbolTabNames 是目前可切換的分頁。私人欄位不列「最近」（個人資料），沒有紀錄時也不列；
// 暫停學習只是不記，照樣列出（同 = 選單的表情順序）。
func (s *Session) symbolTabNames() []string {
	var out []string
	if !s.private && len(s.model.Store.SymbolRecent) > 0 {
		out = append(out, symbols.Recent)
	}
	for _, tab := range symbols.Tabs {
		out = append(out, tab.Name)
	}
	return out
}

func (s *Session) symbolTabItems(name string) []string {
	if name == symbols.Recent {
		recent := symbols.CleanRecent(s.model.Store.SymbolRecent)
		return recent[:min(len(recent), s.pageSize())] // 只放一頁
	}
	for _, tab := range symbols.Tabs {
		if tab.Name == name {
			return tab.Items
		}
	}
	return nil
}

// openSymbolMenu 開符號選單：有最近使用的符號時從「最近」開，否則從第一個分類開。
// start、length、pending 同 openSymbols：選取時替換的區間，pending 表示替換未完成的注音。
func (s *Session) openSymbolMenu(start, length int, pending bool) {
	s.openSymbolTab(s.symbolTabNames()[0], start, length, pending)
}

func (s *Session) openSymbolTab(name string, start, length int, pending bool) {
	items := s.symbolTabItems(name)
	s.openSymbols(items, start, length, pending)
	s.cand.symbols = name
	for _, item := range items {
		s.cand.ids = append(s.cand.ids, symbols.ID(item))
	}
}

// cycleSymbolTab 往後（Tab）或往前（Shift＋Tab）換分頁，頭尾相接；替換的區間不變。
func (s *Session) cycleSymbolTab(step int) {
	c := s.cand
	tabs := s.symbolTabNames()
	at := max(0, slices.Index(tabs, c.symbols))
	s.openSymbolTab(tabs[(at+step+len(tabs))%len(tabs)], c.start, c.length, c.pending)
}

// symbolTitle 是提示列：目前分頁與 Tab 的下一個分頁。↑ 會直接開在某個分類，所以比 = 選單多列目前的分頁。
func (s *Session) symbolTitle() string {
	tabs := s.symbolTabNames()
	at := max(0, slices.Index(tabs, s.cand.symbols))
	return s.cand.symbols + " · Tab → " + tabs[(at+1)%len(tabs)]
}

func (s *Session) symbolMenuKey(k string) bool {
	if s.cand.symbols == "" {
		return false
	}
	switch k {
	case proto.KeyTab:
		s.cycleSymbolTab(1)
	case proto.KeyBackTab:
		s.cycleSymbolTab(-1)
	default:
		return false
	}
	return true
}

// openSymbolMenuAt：組字中游標所在（同 ↓ 的位置規則）是選單裡的符號時，按 ↑ 開完整符號選單，
// 停在那個符號所在的分類、反白它；選了就替換它（#290）。不是選單裡的符號時回傳 false。
func (s *Session) openSymbolMenuAt() bool {
	if s.english || !s.comp.Empty() || s.mixed.raw != "" || len(s.syls) == 0 {
		return false
	}
	pos := min(s.cursor, len(s.syls)-1)
	if s.candidateBefore() {
		pos = max(0, s.cursor-1)
	}
	if !s.literals[pos] {
		return false
	}
	if _, ok := s.syllableLiteralAt(pos); ok {
		return false // 原樣留下的音節是注音文字，↓ 換聲調
	}
	start := s.literalFloor(pos)
	end := s.literalStep(start, 1)
	text := ""
	for _, r := range s.syls[start:end] {
		text += string(r)
	}
	tab, index, ok := symbols.Locate(text)
	if !ok {
		return false
	}
	s.openSymbolTab(symbols.Tabs[tab].Name, start, end-start, false)
	s.cand.cursor = index
	return true
}

// rememberSymbol 記下從符號選單選的符號；私人欄位、暫停學習時不記。
func (s *Session) rememberSymbol(c *candState, text string) {
	if c.symbols == "" || s.paused || s.private || s.localBlocked || !symbols.Contains(text) {
		return
	}
	s.model.RememberSymbol(text)
	s.paletteChanged = true // 個人資料變了，宿主要存檔
}
