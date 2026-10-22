package session

import (
	"slices"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 英數／標點插入時先讓未完成注音占住原本的顯示位置，並保留組音順序。
// 只有這種暫存注音可以還原；使用者從候選明確選入的原樣注音仍是字面文字。
//
// tone 非零時是「沒有字可讀、由聲調鍵完成」而原樣留下的整個音節（#109、#107）：畫面是注音
// 連同聲調（一聲不標），聲調符號緊接在 end() 之後。聲調還在時不還原成未完成注音，免得接著打的
// 聲母蓋掉它；只有聲調符號被刪掉後才變回一般的暫存注音，讓使用者改打別的聲調。
type pendingLiteral struct {
	start    int
	composer zhuyin.Composer
	tone     int // 0：一般暫存注音；1–5：原樣留下的完整音節
	// prefix：音節本身沒有字、只是某個更長的詞的開頭，因此原樣留下（#107 的 ㄟ）。
	// 下一個音節能和它成詞時收回去一起解碼；使用者從 ↓ 明確選的原樣音節不會帶這個標記。
	prefix bool
	// untoned：聲調選填時沒打聲調就完成的音節（#150）；畫面同一聲（不標聲調），收回解碼時仍不限聲調。
	untoned bool
}

func (p pendingLiteral) end() int { return p.start + len([]rune(p.composer.String())) }

// mark 是畫面上聲調符號占的字數：二、三、四聲與輕聲各一個，一聲不標。
func (p pendingLiteral) mark() int {
	if p.tone >= 2 {
		return 1
	}
	return 0
}

func (p pendingLiteral) syllable() zhuyin.Syllable {
	if p.untoned {
		return zhuyin.Untone(zhuyin.Syllable(p.composer.String()))
	}
	return zhuyin.Syllable(p.composer.String()).WithTone(p.tone)
}

func (s *Session) shiftPendingLiterals(at, delta int) {
	if len(s.pendingLiterals) == 0 {
		return
	}
	// 複製而非原地修改，讓無法解碼的音節插入仍能完整回復狀態。
	next := make([]pendingLiteral, 0, len(s.pendingLiterals))
	for _, p := range s.pendingLiterals {
		switch {
		case at < p.start || delta > 0 && at == p.start:
			p.start += delta
		case at < p.end():
			continue // 注音本身已被編輯，不能再還原舊的符號或組音順序。
		case p.mark() > 0 && at == p.end():
			if delta > 0 {
				continue // 注音與聲調之間插進別的字，不再是同一個音節。
			}
			p.tone = 0 // 刪掉聲調：其餘注音回到未完成，可改打別的聲調。
		}
		next = append(next, p)
	}
	s.pendingLiterals = next
}

func (s *Session) restorePendingLiteral() {
	if !s.comp.Empty() {
		return
	}
	for i, p := range s.pendingLiterals {
		if p.tone != 0 || p.end() != s.cursor {
			continue
		}
		s.pendingLiterals = append(s.pendingLiterals[:i:i], s.pendingLiterals[i+1:]...)
		for s.cursor > p.start {
			s.eraseBeforeCursor()
		}
		s.comp = p.composer
		return
	}
}

// insertSyllableLiteral 把沒有字可讀、由聲調鍵完成的音節原樣留在游標處（#109、#107）：
// 注音連同聲調成為字面文字，可見、可退格、Enter 照樣送出；字面文字是解碼邊界，不學習。
// comp 是按聲調之前的組音狀態，接著打的注音從新的音節開始，不會蓋掉它。
func (s *Session) insertSyllableLiteral(comp zhuyin.Composer, syl zhuyin.Syllable) {
	p := pendingLiteral{start: s.cursor, composer: comp, tone: syl.ToneOf()}
	if syl.Untoned() {
		p.tone, p.untoned = 1, true // 沒打聲調（#150）：照一聲的樣子原樣留下，不標聲調
	}
	s.insertLiteral(syl.Text())
	s.pendingLiterals = append(s.pendingLiterals, p)
}

// retypeSyllableLiteral 處理緊接在「沒有字、由聲調鍵完成而原樣留下」的音節之後的注音鍵（#85）：
// 打 ㄍㄨㄜˊ 才發現沒有字，接著打 ㄛ 是要把 ㄜ 改成 ㄛ，不是要在後面開新的音節。這一鍵若取代
// 該音節已有的同類介音或韻母、而且改完的讀音在某個聲調有字，就把音節收回注音暫存（不帶聲調），
// 等使用者重新按聲調，同按聲調之前直接改注音。聲母不取代、原本沒有的介音／韻母不補：刻意留下的
// 原樣注音（ㄏㄟˊ、ㄅ）後面最常接的是新的字（ㄏㄟˊ你、ㄏㄟˊ我、ㄉ一），不能被吃掉。
//
// 按鍵依該音節按聲調前的注音解讀（#183）：許氏打 ㄍㄨㄜˊ 之後的 h 是 ㄛ，不是新音節的 ㄏ。
func (s *Session) retypeSyllableLiteral(key rune) bool {
	if !s.comp.Empty() || s.cursor == 0 {
		return false
	}
	p, ok := s.syllableLiteralAt(s.cursor - 1)
	if !ok || p.prefix || p.end()+p.mark() != s.cursor {
		return false
	}
	comp, sym, ok := s.layout.Press(p.composer, key)
	cl := zhuyin.ClassOf(sym)
	if !ok || cl != zhuyin.Medial && cl != zhuyin.Final {
		return false
	}
	if old := p.composer.Slot(cl); old == 0 || old == sym {
		return false
	}
	base := zhuyin.Syllable(comp.String())
	readable := false
	for tone := 1; tone <= 5 && !readable; tone++ {
		readable = len(s.model.Source().Exact([]zhuyin.Syllable{base.WithTone(tone)})) > 0
	}
	if !readable {
		return false
	}
	for i := range s.pendingLiterals {
		if s.pendingLiterals[i].start == p.start {
			s.pendingLiterals = append(s.pendingLiterals[:i:i], s.pendingLiterals[i+1:]...)
			break
		}
	}
	for s.cursor > p.start {
		s.eraseBeforeCursor()
	}
	s.comp = comp
	return true
}

// syllableState 是插入音節前的組字狀態，插入後解不出來時整個還原。
type syllableState struct {
	syls            []zhuyin.Syllable
	cursor          int
	locks           map[int]decode.Lock
	literals        map[int]bool
	pendingLiterals []pendingLiteral
	mixedKeys       map[int]string
	literalKeys     map[int]string
	mixedOrigins    map[int]mixedOrigin
	displayed       map[int]string
	rejected        []personal.RejectedSeg
}

// 插入、刪除都會換成新的切片與對照表，淺複製就足以還原。
func (s *Session) saveSyllables() syllableState {
	return syllableState{s.syls, s.cursor, s.locks, s.literals, s.pendingLiterals, s.mixedKeys, s.literalKeys, s.mixedOrigins, s.displayed,
		append([]personal.RejectedSeg(nil), s.rejected...)}
}

func (s *Session) restoreSyllables(st syllableState) {
	s.syls, s.cursor, s.locks, s.literals, s.pendingLiterals = st.syls, st.cursor, st.locks, st.literals, st.pendingLiterals
	s.mixedKeys, s.literalKeys, s.mixedOrigins, s.displayed, s.rejected = st.mixedKeys, st.literalKeys, st.mixedOrigins, st.displayed, st.rejected
}

// completeDecodable 回報游標前剛收進的音節所在的一段（字面文字之間）能否解成完整的詞，
// 不算「更長的詞的開頭」那種尾端前綴；開了模糊音時近音也算。
func (s *Session) completeDecodable() bool {
	partial := false // 顯示路徑沒有用前綴蓋到這個音節，就已經是完整的詞，不必再解一次。
	for _, g := range s.path {
		partial = partial || g.Partial && g.Start < s.cursor && s.cursor <= g.End
	}
	if !partial {
		return true
	}
	start, end := s.cursor-1, s.cursor
	for start > 0 && !s.literals[start-1] {
		start--
	}
	for end < len(s.syls) && !s.literals[end] {
		end++
	}
	lat := s.lattice()
	if s.Fuzzy.Enabled() {
		lat = s.Fuzzy.Widen(lat, s.syls, 0, len(s.syls))
		maxCost := s.dec.MaxCost
		s.dec.MaxCost = fuzzy.MaxWordCost
		defer func() { s.dec.MaxCost = maxCost }()
	}
	return len(s.dec.Decode(lat[start:end], s.locksIn(start, end), false)) > 0
}

// prefixLiteralBefore 回傳緊接在游標前、只因「自己沒有字」而原樣留下的音節。
func (s *Session) prefixLiteralBefore() (pendingLiteral, bool) {
	if s.cursor == 0 {
		return pendingLiteral{}, false
	}
	p, ok := s.syllableLiteralAt(s.cursor - 1)
	if !ok || !p.prefix || p.end()+p.mark() != s.cursor {
		return pendingLiteral{}, false
	}
	return p, true
}

// joinPrefixLiteral：游標前是只因「自己沒有字」而原樣留下的音節時，試著把它和剛完成的音節
// 一起收回解碼（ㄋㄧ＋ㄗ˙＝妮子、ㄋㄧ＋ㄨㄟˊ＝妮維…）；接不成詞就維持原樣，回傳 false。
func (s *Session) joinPrefixLiteral(done zhuyin.Syllable) bool {
	p, ok := s.prefixLiteralBefore()
	if !ok {
		return false
	}
	saved := s.saveSyllables()
	for s.cursor > p.start {
		s.eraseBeforeCursor()
	}
	s.insert(p.syllable())
	s.insert(done)
	if s.refresh(); len(s.path) > 0 {
		return true
	}
	s.restoreSyllables(saved)
	s.refresh()
	return false
}

// syllableLiteralAt 回傳涵蓋 pos 的原樣音節（聲調仍在）。
func (s *Session) syllableLiteralAt(pos int) (pendingLiteral, bool) {
	for _, p := range s.pendingLiterals {
		if p.tone != 0 && p.start <= pos && pos < p.end()+p.mark() {
			return p, true
		}
	}
	return pendingLiteral{}, false
}

// openSyllableLiteral 讓 ↓ 把原樣音節當成一個整體：第一項是畫面上帶聲調的原樣注音，
// 其後是同一讀音的其他聲調，同符號選單「同組可重選」的規則；選了照樣是原樣文字。
func (s *Session) openSyllableLiteral(p pendingLiteral) {
	syl := p.syllable()
	items := []string{syl.Text()}
	for tone := 1; tone <= 5; tone++ {
		if tone != p.tone {
			items = append(items, string(syl.WithTone(tone)))
		}
	}
	// 中英混打原樣留下的注音（c␣ 的 ㄏ，#190）：原按鍵排第二個，選了變回英數（比照 #162 出字後的原按鍵）。
	keys := s.mixedKeys[p.start]
	if keys != "" {
		items = slices.Insert(items, 1, keys)
	}
	s.openSymbols(items, p.start, p.end()+p.mark()-p.start, false)
	s.cand.syllable = &p
	if keys != "" {
		s.cand.keyLiterals = map[int]string{1: keys}
	}
}

// pendingStash 是切到英文時清掉的未打完注音（#90），包含中英混打尚未定案、畫面顯示成注音的按鍵。
type pendingStash struct {
	comp  zhuyin.Composer
	mixed pendingKeys
}

// clearPendingZhuyin 清掉未打完的注音，已組好的字保留；沒有可清的內容時回傳 nil。
// 中英混打的暫存按鍵只有畫面顯示成注音時才清，顯示成英數的照舊留成字面文字。
func (s *Session) clearPendingZhuyin() *pendingStash {
	var stash pendingStash
	if s.mixed.raw != "" {
		if _, phonetic := s.mixedPending(s.mixedText()); phonetic {
			stash.mixed = s.mixed
			s.clearMixed()
		}
	}
	if !s.comp.Empty() {
		stash.comp = s.comp
		s.comp.Reset()
	}
	if stash.mixed.raw == "" && stash.comp.Empty() {
		return nil
	}
	return &stash
}
