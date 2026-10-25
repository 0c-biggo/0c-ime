package session

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/emoji"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

type literalSpan struct{ start, end int }

func (s *Session) candidateID(index int) int {
	if s.cand != nil && s.cand.ids != nil {
		if index < 0 || index >= len(s.cand.ids) {
			return -1
		}
		return s.cand.ids[index]
	}
	return index
}

func (s *Session) candidateColumns() int {
	columns := s.CandidateColumns
	if s.styleGrid {
		columns = 5
	}
	if s.cand != nil {
		switch s.cand.kind {
		case "emoji":
			columns = 5
		case "kaomoji":
			columns = 2
		case "phrases":
			columns = 1
		}
	}
	limit := s.selectionKeyColumns()
	// A horizontal row uses the entire page. Named palettes keep their own
	// compact grid, even when ordinary word candidates use a horizontal row.
	if columns > 5 {
		limit = s.pageSize()
	}
	columns = max(1, min(columns, limit))
	if s.displayColumns > 0 {
		columns = min(columns, s.displayColumns)
	}
	return columns
}

func (s *Session) gridMove(key string) {
	c := s.cand
	cols := s.candidateColumns()
	lo := s.candidatePage() * s.pageSize()
	hi := min(lo+s.pageSize(), len(c.items))
	at := c.cursor - lo
	switch key {
	case proto.KeyLeft:
		if at%cols > 0 {
			c.cursor--
		}
	case proto.KeyRight:
		if at%cols+1 < cols && c.cursor+1 < hi {
			c.cursor++
		}
	case proto.KeyUp:
		if at >= cols {
			c.cursor -= cols
		} else if lo > 0 {
			// Previous page's last row, keeping the visual column when present.
			previous := lo - s.pageSize()
			c.cursor = min(previous+(s.pageSize()-1)/cols*cols+at%cols, lo-1)
		} else {
			c.cursor = 0
			s.moveHighlight(-1)
		}
	case proto.KeyDown:
		if lo+(at/cols+1)*cols < hi {
			c.cursor = min(c.cursor+cols, hi-1)
		} else if hi < len(c.items) {
			c.cursor = min(hi+at%cols, len(c.items)-1)
		} else {
			c.cursor = len(c.items) - 1
			s.moveHighlight(1)
		}
	}
}

// Candidate positions count syllables, whereas an undecoded syllable can show
// several phonetic runes. Read the displayed spans rather than slicing preedit
// with a syllable offset; unfinished input at the caret is not in this range.
func (s *Session) candidateText() string {
	start, length := s.cand.span(s.cand.cursor)
	end := start + length
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
		if g.End-g.Start == 1 {
			text.WriteString(g.Word)
		} else {
			runes := []rune(g.Word)
			text.WriteString(string(runes[max(0, start-g.Start):min(len(runes), end-g.Start)]))
		}
	}
	return text.String()
}

// = 選單的分頁：Emoji、顏文字，以及 Preview02 打開（0.2.0 起一律打開）、而且有片語時的「我的片語」（#124）。
var paletteNames = map[string]string{"emoji": "Emoji", "kaomoji": "顏文字", "phrases": "我的片語"}

// phraseIDBase 讓「我的片語」的候選 ID 與表情的目錄 ID（emoji.ID）、等號（emoji.EqualsID）分開，
// 重播時不會混淆；片語本身的編號在 vocab 裡不重用。
const phraseIDBase = 2 << 20

func (s *Session) phrases() []vocab.Phrase {
	if !s.Preview02 || s.model.Vocab == nil {
		return nil
	}
	return s.model.Vocab.Phrases()
}

func (s *Session) paletteTabs() []string {
	tabs := []string{"emoji", "kaomoji"}
	if len(s.phrases()) > 0 {
		tabs = append(tabs, "phrases")
	}
	return tabs
}

// openPalette 開在上次真的選了東西的分頁；那一頁現在不存在（片語刪光）時開 Emoji。
func (s *Session) openPalette() {
	tab := s.model.Store.PaletteTab
	if !slices.Contains(s.paletteTabs(), tab) {
		tab = "emoji"
	}
	s.openEmoji(tab)
}

// cyclePalette 往後（Tab）或往前（Shift＋Tab）換分頁，頭尾相接。
func (s *Session) cyclePalette(step int) {
	tabs := s.paletteTabs()
	at := max(0, slices.Index(tabs, s.cand.kind))
	s.openEmoji(tabs[(at+step+len(tabs))%len(tabs)])
}

func (s *Session) openEmoji(kind string) {
	direct := !s.composing()
	if kind == "phrases" {
		phrases := s.phrases()
		items := make([]string, len(phrases))
		for i, p := range phrases {
			items[i] = p.Text
		}
		s.openSymbols(items, s.cursor, 0, false)
		s.cand.kind, s.cand.direct = kind, direct
		for _, p := range phrases {
			s.cand.ids = append(s.cand.ids, phraseIDBase+int(p.ID))
		}
		return
	}
	items := emoji.Palette(kind, s.model.Store.EmojiRecent)
	if kind == "emoji" && s.EmojiEqualsFirst {
		items = append([]string{"="}, items...)
	}
	s.openSymbols(items, s.cursor, 0, false)
	s.cand.kind, s.cand.direct = kind, direct
	for _, item := range s.cand.items {
		s.cand.ids = append(s.cand.ids, emoji.ID(item.Entry.Surface))
	}
}

// paletteTitle 是提示列：下一個分頁的短名稱（dev.47「提示列過長」的回饋）。
func (s *Session) paletteTitle() string {
	tabs := s.paletteTabs()
	at := max(0, slices.Index(tabs, s.cand.kind))
	return "Tab → " + paletteNames[tabs[(at+1)%len(tabs)]]
}

// phraseLabel 是片語在候選窗顯示的樣子：太長的截斷、換行顯示成 ⏎；選取時插入完整內容。
func phraseLabel(text string) string {
	const max = 24
	text = strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\r", "⏎", "\t", " ").Replace(text)
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return string([]rune(text)[:max-1]) + "…"
}

// A literal English token/path keeps its equals sign. The palette is explicit
// Chinese-mode input, not a replacement inside an ongoing URL or identifier.
func (s *Session) paletteTriggerAllowed() bool {
	if s.mixed.raw != "" && s.mixedText() == s.mixed.raw {
		return false
	}
	_, typedMixed := s.mixedOrigins[s.cursor-1]
	if s.cursor > 0 && typedMixed && s.literals[s.cursor-1] {
		text := string(s.syls[s.cursor-1])
		if len(text) == 1 && !unicode.IsSpace(rune(text[0])) {
			return false
		}
	}
	return true
}

func (s *Session) paletteKey(key string) bool {
	if s.cand.kind == "" {
		return false
	}
	switch key {
	case proto.KeyTab:
		s.cyclePalette(1)
		return true
	case proto.KeyBackTab:
		s.cyclePalette(-1)
		return true
	case "=":
		direct := s.cand.direct
		s.cand = nil
		s.flushMixed()
		s.insertPendingLiteral()
		s.insertLiteral("=")
		s.paletteCommit = direct
		return true
	case proto.KeyBackspace:
		s.cand = nil // close only; no deletion of existing composition.
		return true
	}
	return false
}

func (s *Session) selectEmoji(id int) bool {
	c := s.cand
	text := c.items[id].Entry.Surface
	s.flushMixed()
	s.insertPendingLiteral()
	start := s.cursor
	s.insertLiteral(text)
	if c.kind == "kaomoji" && !c.direct {
		s.paletteUndo = &literalSpan{start, s.cursor}
	}
	if text != "=" && !s.paused && !s.private && !s.localBlocked {
		if c.kind != "phrases" {
			s.model.RememberEmoji(text)
		}
		s.model.RememberPaletteTab(c.kind) // 只有真的選了東西才算；Tab 看看、Esc 取消不改
		s.paletteChanged = true
	}
	s.cand = nil
	// 含換行的片語不留在組字區（外殼的組字區多半畫不了換行）：連同前面的組字一起提交。
	s.paletteCommit = c.direct || c.kind == "phrases" && strings.ContainsAny(text, "\r\n")
	return true
}

// Storage positions remain scalars, preserving phonetic and original-key maps.
// Navigation/deletion across a literal run uses Unicode grapheme boundaries,
// except that a multi-rune symbol (symbolUnits) is one step.
func (s *Session) literalBounds(pos int) []int {
	lo, hi := pos, pos
	for lo > 0 && s.literals[lo-1] {
		lo--
	}
	for hi < len(s.syls) && s.literals[hi] {
		hi++
	}
	if lo == hi {
		return nil
	}
	var text strings.Builder
	for _, scalar := range s.syls[lo:hi] {
		text.WriteString(string(scalar))
	}
	rest := text.String()
	bounds := []int{lo}
	for rest != "" {
		// 「……」「——」這類多字元符號整個算一格（#118）；連打時由左而右兩個一組。
		unit := ""
		for _, u := range symbolUnits {
			if strings.HasPrefix(rest, u) {
				unit = u
				break
			}
		}
		if unit == "" {
			iter := graphemes.FromString(rest)
			iter.Next()
			unit = iter.Value()
		}
		rest = rest[len(unit):]
		lo += utf8.RuneCountInString(unit)
		bounds = append(bounds, lo)
	}
	return bounds
}

func (s *Session) literalStep(pos, direction int) int {
	for _, at := range s.literalBounds(pos) {
		if direction > 0 && at > pos {
			return at
		}
	}
	if direction < 0 {
		bounds := s.literalBounds(pos)
		for i := len(bounds) - 1; i >= 0; i-- {
			if bounds[i] < pos {
				return bounds[i]
			}
		}
	}
	return max(0, min(pos+direction, len(s.syls)))
}

func (s *Session) literalFloor(pos int) int {
	previous := pos
	for _, at := range s.literalBounds(pos) {
		if at == pos {
			return pos
		}
		if at > pos {
			return previous
		}
		previous = at
	}
	return pos
}
