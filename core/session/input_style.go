package session

import (
	"slices"
	"unicode"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// insertStyleLiteral preserves the order of the visible pending English or
// mixed-input run before inserting a symbol at its cursor.
func (s *Session) insertStyleLiteral(text string) {
	s.flushMixed()
	s.insertPendingLiteral()
	s.insertLiteral(text)
}

func (s *Session) fullWidthText(k string) string {
	if s.InputStyle.Punctuation == "microsoft" && k == "'" {
		return "’"
	}
	if s.InputStyle.Punctuation == "chewing" {
		switch k {
		case "\"":
			return "”"
		case "'":
			return "’"
		case "[":
			return "〔"
		case "]":
			return "〕"
		}
	}
	return fullWidthASCII(k)
}

func (s *Session) pageSize() int {
	if s.InputStyle.CandidatePageSize == 9 {
		return 9
	}
	return 10
}

func fullWidthASCII(k string) string {
	r := []rune(k)
	for i, c := range r {
		if c == ' ' {
			r[i] = '\u3000'
		} else if c >= '!' && c <= '~' {
			r[i] += 0xfee0
		}
	}
	return string(r)
}

// styleKey handles only explicit profile differences. The zero rules object
// never changes legacy key routing. Width is session state, not a saved option.
func (s *Session) styleKey(k string) (handled bool, commit string, done bool) {
	if s.InputStyle.Version == 0 {
		return false, "", false
	}
	if k == proto.KeyShiftSpace {
		switch s.InputStyle.ShiftSpace {
		case "fullwidth":
			s.fullWidth = !s.fullWidth
			return true, "", true
		case "space":
			if s.cand != nil {
				s.cand.cursor = (s.candidatePage() + 1) % s.pages() * s.pageSize()
				return true, "", true
			}
			if !s.composing() {
				return true, " ", true
			}
			if s.cursor == len(s.syls) {
				return true, s.commit() + " ", true
			}
			s.insertStyleLiteral(" ")
			return true, "", true
		case "single":
			s.openCandidates(1)
			return s.composing(), "", true
		default:
			return s.composing(), "", true
		}
	}
	if s.symbolPrefix {
		if len([]rune(k)) != 1 && k != proto.KeyEscape && k != proto.KeyBackspace {
			return false, "", false
		}
		s.symbolPrefix = false
		if k == proto.KeyEscape || k == proto.KeyBackspace {
			return true, "", true
		}
		if p, ok := microsoftPrefixPunctuation(k); ok {
			s.insertStyleLiteral(p)
			return true, "", true
		}
		if p, ok := proto.CtrlPunct(proto.CtrlPrefix + k); ok {
			s.insertStyleLiteral(p)
			return true, "", true
		}
		if r := []rune(k); len(r) == 1 {
			if p, ok := s.layout.Punct(r[0]); ok {
				s.insertStyleLiteral(p)
				return true, "", true
			}
		}
		if r := []rune(k); len(r) == 1 && unicode.IsPrint(r[0]) {
			s.insertStyleLiteral(s.fullWidthText(k))
			return true, "", true
		}
	}
	// English autocomplete suggestions are advisory: a printable full-width
	// key ends that pending word without accepting its highlighted completion.
	if s.fullWidth && s.english && s.cand != nil && s.cand.english {
		if r := []rune(k); len(r) == 1 && unicode.IsPrint(r[0]) {
			s.cand = nil
		}
		if _, ok := proto.KeypadChar(k); ok {
			s.cand = nil
		}
	}
	if s.cand != nil {
		if s.InputStyle.CandidateNavigation == "horizontal" && s.cand.kind == "" {
			switch k {
			case proto.KeyLeft:
				s.moveHighlight(-1)
				return true, "", true
			case proto.KeyRight:
				s.moveHighlight(1)
				return true, "", true
			case proto.KeyUp:
				s.cand.cursor = (s.candidatePage() + s.pages() - 1) % s.pages() * s.pageSize()
				return true, "", true
			case proto.KeyDown:
				s.cand.cursor = (s.candidatePage() + 1) % s.pages() * s.pageSize()
				return true, "", true
			}
		}
		if s.InputStyle.CandidateTab == "grid" && k == proto.KeyRight && !s.styleGrid {
			s.styleGrid = true
			return true, "", true
		}
		return false, "", false
	}
	// Apple calls the backward-delete key Delete; accept both physical variants.
	if (k == proto.KeyShiftDelete || k == proto.KeyShiftBackspace) && !s.english && s.InputStyle.ShiftDelete == "tone" {
		if !s.comp.Empty() {
			s.comp = s.layout.Backspace(s.comp)
			return true, "", true
		}
		if s.cursor > 0 && !s.literals[s.cursor-1] {
			syl := s.syls[s.cursor-1]
			s.eraseBeforeCursor()
			for _, r := range string(syl) {
				if zhuyin.ClassOf(r) != zhuyin.Tone {
					s.comp.Add(r)
				}
			}
			return true, "", true
		}
		return s.composing(), "", true
	}
	if !s.english && k == "`" && s.InputStyle.Punctuation == "microsoft" {
		s.flushMixed()
		s.symbolPrefix = true
		return true, "", true
	}
	if !s.english && ((k == proto.KeyTab || k == proto.KeyBackTab) && s.InputStyle.Tab == "cycle" || k == proto.KeyUp && s.InputStyle.Up == "cycle") {
		if !s.composing() {
			return false, "", true
		}
		if s.comp.Empty() {
			d := 1
			if k == proto.KeyBackTab {
				d = -1
			}
			s.cycleStyleCandidate(d)
		}
		return true, "", true
	}
	if !s.english && k == proto.KeyTab && s.InputStyle.Tab == "segment" {
		if !s.composing() {
			return false, "", true
		}
		if s.comp.Empty() {
			s.changeSegmentation()
		}
		return true, "", true
	}
	if c, ok := proto.KeypadChar(k); ok && s.fullWidth {
		if !s.composing() {
			return true, s.fullWidthText(string(c)), true
		}
		s.insertStyleLiteral(s.fullWidthText(string(c)))
		return true, "", true
	}
	r := []rune(k)
	if len(r) != 1 || !unicode.IsPrint(r[0]) {
		return false, "", false
	}
	if s.InputStyle.Punctuation == "microsoft" && microsoftRawPunctuation(k) {
		if s.fullWidth {
			// Microsoft keeps wide punctuation as marked text even in English
			// mode; unlike wide letters/digits it can still be cancelled.
			s.insertStyleLiteral(s.fullWidthText(k))
			return true, "", true
		}
		if !s.english && !s.comp.Empty() {
			// Half-width raw punctuation cancels an unfinished syllable.
			// Already converted words remain in their original order.
			s.comp.Reset()
			if !s.composing() {
				return true, k, true
			}
		}
		if !s.composing() {
			return false, "", true
		}
		s.insertStyleLiteral(k)
		return true, "", true
	}
	if s.fullWidth && s.english {
		if !s.composing() {
			return true, s.fullWidthText(k), true
		}
		s.insertStyleLiteral(s.fullWidthText(k))
		return true, "", true
	}
	if s.english {
		// The shell normalizes Caps to lowercase and Caps+Shift to uppercase.
		// Consume both: passing uppercase through would replay the host's
		// original Caps+Shift lowercase event on Windows and Linux.
		if s.InputStyle.CapsLetter == "lower" && s.capsLock && ((r[0] >= 'a' && r[0] <= 'z') || (r[0] >= 'A' && r[0] <= 'Z')) {
			if !s.composing() {
				return true, k, true
			}
			s.insertStyleLiteral(k)
			return true, "", true
		}
		return false, "", false
	}

	if r[0] >= 'A' && r[0] <= 'Z' && s.InputStyle.ShiftLetter == "commit" {
		return true, s.commit() + k, true
	}
	if s.fullWidth && !s.english {
		_, phonetic := s.layout.Symbol(r[0])
		_, punct := s.layout.Punct(r[0])
		if !phonetic && !punct && r[0] >= '!' && r[0] <= '~' {
			if !s.composing() {
				return true, s.fullWidthText(k), true
			}
			s.insertStyleLiteral(s.fullWidthText(k))
			return true, "", true
		}
	}
	if s.fullWidth && (r[0] >= 'A' && r[0] <= 'Z' || k == " " && !s.composing()) {
		if !s.composing() {
			return true, s.fullWidthText(k), true
		}
		s.insertStyleLiteral(s.fullWidthText(k))
		return true, "", true
	}
	if (s.InputStyle.Punctuation == "standard" || s.InputStyle.Punctuation == "chewing") && k == "\\" {
		s.insertStyleLiteral("＼")
		return true, "", true
	}
	return false, "", false
}

func (s *Session) cycleStyleCandidate(delta int) {
	s.refresh()
	continuing := s.styleCycle != nil
	if continuing {
		s.cand = s.styleCycle
	} else {
		s.openCandidates(0)
	}
	if s.cand == nil {
		return
	}
	c := s.cand
	limit := len(c.items)
	if c.split > 0 {
		limit = c.split
	}
	if c.raw && c.split == 0 {
		limit--
	}
	if limit <= 0 {
		s.cand = nil
		return
	}
	start, length := c.start, c.length
	var current string
	for _, g := range s.path {
		if g.Start <= start && g.End >= start+length {
			current = string([]rune(g.Word)[start-g.Start : start-g.Start+length])
			break
		}
	}
	i := -1
	if continuing {
		i = c.cursor
	}
	for n := 0; !continuing && n < limit; n++ {
		if c.items[n].Entry.Surface == current {
			i = n
			break
		}
	}
	if i < 0 {
		i = 0
	}
	id := (i + delta + limit) % limit
	c.cursor = id
	s.styleCycle = c
	cursor, advance := s.cursor, s.AdvanceAfterSelect
	s.AdvanceAfterSelect = false
	s.selectCandidate(s.candidateID(id))
	s.AdvanceAfterSelect, s.cursor = advance, cursor
}

// changeSegmentation keeps the displayed characters, changing only lexical
// boundaries. At a boundary Tab joins adjacent words; inside a word it splits.
// At the end it cycles the feasible segmentations of the displayed sentence.
func (s *Session) changeSegmentation() {
	s.refresh()
	if len(s.syls) < 2 || len(s.literals) > 0 {
		return
	}
	var text []rune
	for _, g := range s.path {
		text = append(text, []rune(g.Word)...)
	}
	if len(text) != len(s.syls) {
		return
	}
	var choices [][]int
	visited := 0
	var walk func(int, []int)
	walk = func(start int, ends []int) {
		visited++
		if len(choices) >= 64 || visited > 2048 {
			return
		}
		if start == len(text) {
			choices = append(choices, slices.Clone(ends))
			return
		}
		for end := min(len(text), start+decode.MaxLen(s.dec.Lex)); end > start; end-- {
			word := string(text[start:end])
			for _, e := range s.dec.Lex.Exact(s.syls[start:end]) {
				if e.Surface == word {
					walk(end, append(ends, end))
					break
				}
			}
		}
	}
	walk(0, nil)
	if len(choices) < 2 {
		return
	}
	current := []int{}
	for _, g := range s.path {
		current = append(current, g.End)
	}
	var selected []int
	if s.cursor == len(text) {
		for i, ends := range choices {
			if slices.Equal(ends, current) {
				selected = choices[(i+1)%len(choices)]
				break
			}
		}
	} else if s.cursor > 0 {
		wantBreak := !slices.Contains(current, s.cursor)
		best := len(text) + 1
		for _, ends := range choices {
			if slices.Contains(ends, s.cursor) != wantBreak {
				continue
			}
			diff := 0
			for i := 1; i < len(text); i++ {
				if slices.Contains(ends, i) != slices.Contains(current, i) {
					diff++
				}
			}
			if diff < best {
				selected = ends
				best = diff
			}
		}
	}
	if selected == nil {
		return
	}
	s.locks = map[int]decode.Lock{}
	start := 0
	for _, end := range selected {
		s.locks[start] = decode.Lock{Word: string(text[start:end]), Len: end - start, Segmentation: true}
		start = end
	}
	s.pathSig = ""
	s.refresh()
}

// These raw and prefix mappings were observed in the isolated Windows 11
// Microsoft Bopomofo reference run; unobserved keys retain their prior routing.
func microsoftRawPunctuation(k string) bool {
	switch k {
	case "\\", "'", "\"", "<", ">", "?":
		return true
	}
	return false
}
func microsoftPrefixPunctuation(k string) (string, bool) {
	switch k {
	case "\\":
		return "＼", true
	case "'":
		return "、", true
	case "\"":
		return "＂", true
	case "<":
		return "＜", true
	case ">":
		return "＞", true
	case "?":
		return "？", true
	}
	return "", false
}
