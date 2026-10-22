package session

import (
	"slices"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/suggest"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 同組符號可用 ↓ 重選；左右括號分組，避免把開括號換成閉括號。每一項是一個符號，可以是多個字元
// （刪節號「……」、破折號「——」各算一個，見 symbolUnits）。
//
// 符號出現在多組時，以最後一組為它的本組：↓ 列本組，符號選單也只在本組的位置列一次。
// 所以「…」「⋯」歸刪節號組、「—」「－」歸破折號組、「←」「→」歸箭頭組（#118）。
// ^、_ 兩組參考新酷音（libchewing symbol.rs）與小麥注音（BPMFPunctuations.txt）的 ^、_ 符號組，
// 最後附半形原字元，讓中文模式要打半形 ^、_ 的人換得回來。
// 斜線和問號同一個實體鍵：標準排列的 / 是 ㄥ，Shift＋/ 打出「？」後按 ↓ 換全形「／」或半形「/」（#148）。
var symbolGroups = [][]string{
	runes("「『﹁﹃“‘"), runes("」』﹂﹄”’"),
	runes("（〔【〈《［｛"), runes("）〕】〉》］｝"),
	runes("，、﹐､"), runes("。．｡⋯…"), runes("？！﹖﹗／/"), runes("：；﹕﹔"), runes("～〜—–－"), runes("＆&﹠"),
	{"……", "⋯⋯", "…", "⋯", "︿", "^"},
	{"——", "—", "―", "─", "－", "＿", "←", "→", "_"},
	runes("←→↑↓"),
	runes("ㄅㄆㄇㄈㄉㄊㄋㄌㄍㄎㄏㄐㄑㄒㄓㄔㄕㄖㄗㄘㄙㄧㄨㄩㄚㄛㄜㄝㄞㄟㄠㄡㄢㄣㄤㄥㄦˉˊˇˋ˙"),
}

// symbolUnits 是多字元的符號：在組字區裡像一個字一樣移動、刪除，按 ↓ 整個換掉。
var symbolUnits = func() []string {
	var out []string
	for _, group := range symbolGroups {
		for _, item := range group {
			if utf8.RuneCountInString(item) > 1 && !slices.Contains(out, item) {
				out = append(out, item)
			}
		}
	}
	return out
}()

func runes(group string) []string {
	var out []string
	for _, r := range group {
		out = append(out, string(r))
	}
	return out
}

// symbolHome 回傳符號的本組（最後一個列出它的組）；不在任何組時回傳 -1。
func symbolHome(item string) int {
	for i := len(symbolGroups) - 1; i >= 0; i-- {
		if slices.Contains(symbolGroups[i], item) {
			return i
		}
	}
	return -1
}

func symbolPalette() []string {
	var out []string
	for i, group := range symbolGroups {
		for _, item := range group {
			if symbolHome(item) == i {
				out = append(out, item)
			}
		}
	}
	return out
}

func symbolAlternatives(current string) []string {
	out := []string{current}
	if home := symbolHome(current); home >= 0 {
		for _, item := range symbolGroups[home] {
			if item != current {
				out = append(out, item)
			}
		}
	}
	return out
}

func rawCandidate(text string) decode.Candidate {
	return decode.Candidate{Entry: lexicon.Entry{Surface: text}}
}

func (s *Session) openSymbols(items []string, start, length int, pending bool) {
	c := &candState{start: start, length: length, literal: true, pending: pending}
	for _, text := range items {
		c.items = append(c.items, rawCandidate(text))
	}
	s.cand = c
}

func (s *Session) insertLiteral(text string) {
	if text == "" {
		return
	}
	s.refresh()
	at := s.cursor
	var split *decode.Seg
	for _, g := range s.path {
		if g.Start < at && at < g.End {
			copy := g
			split = &copy
			break
		}
	}
	// 被切開的詞若含跨音鎖定，兩半各帶走自己的詞庫讀音；要在 insert 改動鎖定之前算好。
	var leftMatched, rightMatched string
	if split != nil {
		leftMatched = s.matchedFor(split.Start, at-split.Start)
		rightMatched = s.matchedFor(at, split.End-at)
	}
	for _, r := range text {
		at := s.cursor
		s.insert(zhuyin.Syllable(string(r)))
		if s.literals == nil {
			s.literals = map[int]bool{}
		}
		s.literals[at] = true
	}
	if split != nil {
		// 插入英數只改文字位置，不能趁斷詞把「麻煩」重選成「麻1凡」。
		word, cut := []rune(split.Word), at-split.Start
		s.locks[split.Start] = decode.Lock{Word: string(word[:cut]), Len: cut, LiteralBoundary: 1, Matched: leftMatched}
		s.locks[s.cursor] = decode.Lock{Word: string(word[cut:]), Len: split.End - at, LiteralBoundary: -1, Matched: rightMatched}
	}
}

// insertPunctKey 在游標處插入標點鍵 k 打出的全形標點 p。中英混打時 Shift＋, Shift＋. 的「，」「。」記下原按鍵，
// ↓ 第二項是半形 < >（#190）：混打的注音鍵、數字鍵出字後 ↓ 第二項都是原按鍵（#162、#175、#187），這兩個鍵比照辦理，
// 不切英文也打得出大於、小於。只限這兩個鍵在混打下；一般注音模式與其他標點維持全形（#177），Ctrl＋標點（#168）也不附。
func (s *Session) insertPunctKey(k, p string) {
	s.insertLiteral(p)
	if s.mixedInput() && (k == "<" || k == ">") {
		s.setLiteralKey(s.cursor-1, k)
	}
}

func (s *Session) insertPendingLiteral() {
	if s.comp.Empty() || s.finishUntoned() { // 聲調選填：已是完整音節的注音不打聲調就完成，不留成符號（#150）
		return
	}
	pending := pendingLiteral{start: s.cursor, composer: s.comp}
	text := s.comp.String()
	s.comp.Reset()
	s.insertLiteral(text)
	s.pendingLiterals = append(s.pendingLiterals, pending)
}

func (s *Session) shiftLiterals(at, delta int) {
	if len(s.literals) == 0 {
		return
	}
	next := map[int]bool{}
	for pos := range s.literals {
		switch {
		case pos < at:
			next[pos] = true
		case delta > 0 || pos > at:
			next[pos+delta] = true
		}
	}
	s.literals = next
}

func (s *Session) selectLiteral(id int) bool {
	if s.cand.kind != "" {
		return s.selectEmoji(id)
	}
	c := s.cand
	text := c.items[id].Entry.Surface
	if c.pending {
		s.comp.Reset()
	}
	// 先移除被選區間，再逐字插入；游標停在新符號後，可直接繼續輸入。
	start, length := c.span(id) // 混合清單的原樣注音只替換游標那個字（#161）
	s.cursor = start + length
	for range length {
		s.eraseBeforeCursor()
	}
	if c.keyLiterals[id] != "" {
		s.insertMixedLiteral(text, false, true)
	} else if c.syllable != nil {
		s.insertSyllableLiteral(c.syllable.composer, zhuyin.Syllable(text))
	} else {
		s.insertLiteral(text)
	}
	s.cand = nil
	return true
}

// 字面符號是解碼邊界，兩側的中文各自從句首計算，不把標點塞進詞庫或語言模型。
func (s *Session) decodeWithLiterals() []decode.Seg {
	s.configureDocumentContext(0)
	lat := s.lattice()
	s.basePath = nil
	// 移動游標只改變編輯位置，不能因此換一套解碼、改寫畫面上的字。
	prefix := s.Opt.PrefixAware
	if len(s.literals) == 0 {
		s.dec.Evidence = nil
		s.configureCorrections(0, len(s.syls), false)
		s.basePath = s.dec.Decode(lat, s.locks, prefix)
		s.dec.Evidence = s.localEvidence(0)
		s.configureCorrections(0, len(s.syls), true)
		if s.dec.Evidence == nil && len(s.locks) == 0 {
			return s.basePath
		}
		return s.dec.Decode(lat, s.locks, prefix)
	}
	var path []decode.Seg
	for start := 0; start < len(lat); {
		if s.literals[start] {
			path = append(path, decode.Seg{Word: string(s.syls[start]), Start: start, End: start + 1})
			start++
			continue
		}
		end := start + 1
		for end < len(lat) && !s.literals[end] {
			end++
		}
		locks := s.locksIn(start, end)
		// 標點前保留原本尾端的前綴顯示，避免「麻」加標點突然被換成「嘛」。
		s.configureDocumentContext(start)
		s.dec.Evidence = nil
		s.configureCorrections(start, end, false)
		part := s.dec.Decode(lat[start:end], locks, prefix)
		for _, g := range part {
			g.Start += start
			g.End += start
			s.basePath = append(s.basePath, g)
		}
		s.dec.Evidence = s.localEvidence(start)
		s.configureCorrections(start, end, true)
		if s.dec.Evidence != nil || len(s.locks) > 0 {
			part = s.dec.Decode(lat[start:end], locks, prefix)
		}
		if len(part) == 0 {
			return nil // 保留既有的原樣注音保底，任何文字都不消失
		}
		for _, seg := range part {
			seg.Start, seg.End = seg.Start+start, seg.End+start
			path = append(path, seg)
		}
		start = end
	}
	return path
}

func (s *Session) suggestAfterLiteral() *suggest.Suggestion {
	opts := s.Opt.Suggest
	opts.RewritePrefix = opts.RewritePrefix || s.previewRewrite
	start := 0
	for pos := range s.literals {
		start = max(start, pos+1)
	}
	s.configureDocumentContext(start)
	s.configureCorrections(start, len(s.syls), true)
	s.dec.CorrectionUsage = s.model.CorrectionUsage(s.syls[start:])
	if start == 0 {
		return suggest.ForWithLocks(s.dec, s.lattice(), s.path, s.locks, opts)
	}
	var path []decode.Seg
	for _, seg := range s.path {
		if seg.Start >= start {
			seg.Start, seg.End = seg.Start-start, seg.End-start
			path = append(path, seg)
		}
	}
	locks := map[int]decode.Lock{}
	for at, lock := range s.locks {
		if at >= start {
			locks[at-start] = lock
		}
	}
	g := suggest.ForWithLocks(s.dec, s.lattice()[start:], path, locks, opts)
	if g != nil {
		g.Start += start
	}
	return g
}
