package session

import (
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

const maxMixedPending = 64

// pendingKeys keeps the physical spelling until a tone/boundary resolves it.
// Neither a repeated phonetic slot nor a proposed correction overwrites keys.
type pendingKeys struct {
	raw, accepted  string
	choice         int // 0: infer, 1: literal, 2: phonetic (explicit selection)
	assistedPrefix int // raw 的前段保留了已接受補字；後續追加字元仍是自行輸入
}

// mixedComposer rejects lossy same-slot replacement while both languages compete.
// Explicit phonetic symbols still use the ordinary Composer replacement rules.
func (s *Session) mixedComposer(raw string) (zhuyin.Composer, bool) {
	var c zhuyin.Composer
	seen := map[zhuyin.Class]bool{}
	for _, k := range raw {
		sym, ok := s.layout.Symbol(k)
		cl := zhuyin.ClassOf(sym)
		if !ok || cl == zhuyin.None || cl == zhuyin.Tone || seen[cl] {
			return zhuyin.Composer{}, false
		}
		seen[cl] = true
		c.Add(sym)
	}
	return c, raw != ""
}

func (s *Session) mixedText() string {
	p := s.mixed
	if p.accepted != "" {
		return p.accepted
	}
	if s.englishWord {
		return p.raw
	}
	// 使用者加的英文詞（ls、rm、kubectl，#146）：完全相同（大小寫照字面）就照字面，優先於一聲讀音與注音解釋。
	// ↓ 選了注音解釋（choice 2）照舊顯示注音。
	if p.choice == 0 && s.vocabEnglish(p.raw) {
		return p.raw
	}
	// 沒有字母（只有數字與標點鍵，也是 ㄅㄉㄓㄚㄞㄢ、ㄝㄡㄥㄤㄦ）時照字面顯示與提交：「我.」送出「我.」不是「我ㄡ」，
	// 「2 」是「2 」不是「ㄉ」，10:30 不變成「ㄅㄢ：3安」。接聲調仍組成中文（.3 → 偶、184 → 爸），↓ 也仍可選注音解釋。
	if p.choice == 0 && !strings.ContainsFunc(p.raw, unicode.IsLetter) {
		return p.raw
	}
	// 數字與字母混在一起、任何聲調都組不成音節（5pm、3d）：照字面，不顯示成「ㄓㄩㄣ」再整串送出注音。
	if p.choice == 0 && strings.ContainsFunc(p.raw, unicode.IsDigit) && !s.mixedReachable(p.raw) {
		return p.raw
	}
	c, phonetic := s.mixedComposer(p.raw)
	englishPrior := len(p.raw) >= 2 && len(s.englishPrefix(p.raw, 1)) > 0
	if len(p.raw) == 1 && s.mixedEnglishWord(p.raw) {
		englishPrior = true
	}
	// A first-tone reading (up → ㄧㄣ, co → ㄏㄟ, i → ㄛ) must not disappear
	// merely because these physical keys also prefix an English word.
	firstTone := false
	if phonetic && englishPrior && !mixedLetterWord(p.raw) && p.choice == 0 {
		_, firstTone = s.mixedReading(p.raw, zhuyin.Tone1)
	}
	if phonetic && p.choice != 1 && (p.choice == 2 || !englishPrior || firstTone) {
		return c.String()
	}
	return p.raw
}

func (s *Session) clearMixed() {
	s.mixed = pendingKeys{}
	s.englishWord = false
}

func (s *Session) insertMixedReading(syl zhuyin.Syllable, keys string) {
	pos := s.cursor
	s.insert(syl)
	if s.mixedKeys == nil {
		s.mixedKeys = map[int]string{}
	}
	s.mixedKeys[pos] = keys
}

// insertMixedTyped 收進中英混打以聲調鍵完成的音節。自己沒有字、只能當更長的詞開頭的音節
// （ㄟ 只有「工口」），和一般注音模式一樣原樣留下注音，下一個音節能和它成詞時再收回（#107）。
func (s *Session) insertMixedTyped(syl zhuyin.Syllable, keys string) {
	if s.joinPrefixLiteral(syl) {
		if s.mixedKeys == nil {
			s.mixedKeys = map[int]string{}
		}
		s.mixedKeys[s.cursor-1] = keys
		return
	}
	s.refresh()
	decodable := len(s.syls) == 0 || len(s.path) > 0 || s.nearReading
	saved := s.saveSyllables()
	s.insertMixedReading(syl, keys)
	if s.refresh(); decodable && len(s.path) > 0 && !s.completeDecodable() {
		s.restoreSyllables(saved)
		var comp zhuyin.Composer
		for _, r := range string(syl.Base()) {
			comp.Add(r)
		}
		s.insertSyllableLiteral(comp, syl)
		s.pendingLiterals[len(s.pendingLiterals)-1].prefix = true
	}
}

func (s *Session) shiftMixedKeys(at, delta int) {
	s.mixedKeys = shiftKeys(s.mixedKeys, at, delta)
}

func (s *Session) originalMixedKeys(pos, length int) string {
	var keys strings.Builder
	for at := pos; at < pos+length; at++ {
		if s.mixedKeys[at] == "" {
			return ""
		}
		keys.WriteString(s.mixedKeys[at])
	}
	return keys.String()
}

// mixedPending 回報暫存按鍵目前是否顯示成未打完的注音（text 為 mixedText），並回傳其組音狀態。
func (s *Session) mixedPending(text string) (zhuyin.Composer, bool) {
	c, ok := s.mixedComposer(s.mixed.raw)
	return c, !s.englishWord && ok && s.mixed.accepted == "" && text == c.String()
}

// Flushing preserves exactly what is displayed. An unfinished phonetic segment
// uses the existing pending-literal restoration mechanism for cursor edits.
func (s *Session) flushMixed() {
	if s.mixed.raw == "" {
		return
	}
	text := s.mixedText()
	c, phonetic := s.mixedPending(text)
	if !phonetic {
		s.reviseLetterI(text)
	}
	previous, at := slices.Clone(s.path), s.cursor
	before := len(s.syls)
	assisted, explicit := s.mixed.accepted != "", s.mixed.choice == 1
	assistedPrefix := s.mixed.assistedPrefix
	s.clearMixed()
	if phonetic {
		s.comp = c
		s.insertPendingLiteral()
	} else {
		s.insertMixedLiteral(text, assisted, explicit)
		for pos := at; pos < at+min(assistedPrefix, len([]rune(text))); pos++ {
			origin := s.mixedOrigins[pos]
			origin.assisted = true
			s.mixedOrigins[pos] = origin
		}
	}
	s.refresh() // Enter must include newly materialized literals even with neural display caching.
	s.keepMixedDisplay(previous, at, len(s.syls)-before)
}

// reviseLetterI（#69）：「i␣」先當成「喔」；緊接著打出的是完整英文單字（i am、i think、
// i love you）時，回頭改成英文 i 加空白。句尾或後面接中文的「i␣」照舊是喔；i7 是明確的聲調，不改。
// 使用者選過候選（鎖定）的字也不改。
func (s *Session) reviseLetterI(word string) {
	at := s.cursor - 1
	if len([]rune(word)) < 2 || !s.mixedEnglishWord(word) || at < 0 || s.literals[at] || s.mixedKeys[at] != "i " {
		return
	}
	for start, lock := range s.locks {
		if start <= at && at < start+lock.Len {
			return
		}
	}
	s.eraseBeforeCursor()
	s.insertMixedLiteral("i ", false, false)
	s.refresh()
}

// Materializing already visible pending text is not a new Chinese input event.
// Preserve its surrounding displayed words, including neural decisions, without
// manufacturing manual-selection locks or performing another inference on Enter.
func (s *Session) keepMixedDisplay(previous []decode.Seg, at, delta int) {
	if len(previous) == 0 || delta <= 0 {
		return
	}
	var path []decode.Seg
	inserted := false
	add := func() {
		for pos := at; pos < at+delta; pos++ {
			path = append(path, decode.Seg{Word: string(s.syls[pos]), Start: pos, End: pos + 1})
		}
		inserted = true
	}
	for _, g := range previous {
		switch {
		case g.End <= at:
			path = append(path, g)
		case g.Start >= at:
			if !inserted {
				add()
			}
			g.Start += delta
			g.End += delta
			path = append(path, g)
		default:
			word, cut := []rune(g.Word), at-g.Start
			left, right := g, g
			left.Word, left.End = string(word[:cut]), at
			right.Word, right.Start, right.End = string(word[cut:]), at+delta, g.End+delta
			path = append(path, left)
			add()
			path = append(path, right)
		}
	}
	if !inserted {
		add()
	}
	s.path = path
	s.shownPath, s.shownSyls = slices.Clone(path), slices.Clone(s.syls)
	for _, g := range path {
		for i, r := range []rune(g.Word) {
			if !s.literals[g.Start+i] {
				s.displayed[g.Start+i] = string(r)
			}
		}
	}
}

func (s *Session) mixedSuggestion() string {
	p := s.mixed
	if p.raw == "" || p.accepted != "" || p.choice == 2 || s.mixedText() != p.raw || len(p.raw) < 2 || s.vocabEnglish(p.raw) {
		return ""
	}
	for _, c := range s.englishPrefix(p.raw, 5) {
		if len(c.Text) >= len(p.raw)+2 {
			return c.Text
		}
	}
	if cs := s.englishCorrections(p.raw, 1); len(cs) > 0 {
		return cs[0].Text
	}
	return ""
}

func (s *Session) acceptMixed() bool {
	if s.cand != nil {
		return false
	}
	word := s.mixedSuggestion()
	if word == "" {
		return false
	}
	s.mixed.accepted = word
	return true
}

func (s *Session) openMixedCandidates() {
	if s.mixed.raw == "" {
		return
	}
	c := &candState{start: s.cursor, mixed: true}
	seen := map[string]bool{}
	add := func(text string) {
		if text != "" && !seen[text] {
			seen[text] = true
			c.items = append(c.items, rawCandidate(text))
		}
	}
	add(s.mixed.raw) // The original keys are always reachable.
	if comp, ok := s.mixedComposer(s.mixed.raw); ok {
		add(comp.String())
	}
	// 結尾是聲調鍵、但組不成中文而照字面顯示的按鍵（co6）：混打照舊顯示原按鍵，↓ 另給帶聲調的
	// 原樣注音（ㄏㄟˊ），與一般模式原樣留下的音節相同（#109）。
	if raw := s.mixed.raw; len(raw) > 1 {
		if sym, ok := s.layout.Symbol(rune(raw[len(raw)-1])); ok && zhuyin.ClassOf(sym) == zhuyin.Tone {
			if comp, ok := s.mixedComposer(raw[:len(raw)-1]); ok {
				if done, ok := comp.Add(sym); ok {
					add(string(done))
				}
			}
		}
	}
	for _, word := range s.englishPrefix(s.mixed.raw, 5) {
		add(word.Text)
	}
	for _, word := range s.englishCorrections(s.mixed.raw, 3) {
		add(word.Text)
	}
	s.cand = c
}

func (s *Session) selectMixed(id int) bool {
	if s.cand == nil || id < 0 || id >= len(s.cand.items) {
		return false
	}
	text := s.cand.items[id].Entry.Surface
	s.mixed.accepted = ""
	s.mixed.choice = 1
	if text != s.mixed.raw {
		if comp, ok := s.mixedComposer(s.mixed.raw); ok && text == comp.String() {
			s.mixed.choice = 2
		} else {
			s.mixed.accepted = text
		}
	}
	s.cand = nil
	return true
}

// A complete phonetic reading must be reachable in the current Chinese context.
// This also admits a syllable available only as part of a multi-syllable word.
func (s *Session) mixedReading(raw string, tone rune) (zhuyin.Syllable, bool) {
	c, ok := s.mixedComposer(raw)
	if !ok {
		return "", false
	}
	done, ok := c.Add(tone)
	if !ok {
		return "", false
	}
	done = s.neutralFallback(done)
	return done, s.readingReachable(done)
}

// readingReachable reports whether a syllable typed at the cursor can start or
// continue a dictionary word, alone or after the preceding Chinese syllables.
func (s *Session) readingReachable(done zhuyin.Syllable) bool {
	return len(s.model.Source().Exact([]zhuyin.Syllable{done})) > 0 || s.model.Source().HasPrefix([]zhuyin.Syllable{done}) ||
		s.readingContinues(done)
}

// readingContinues：音節接在游標前面的中文音節後面，成為詞庫裡的詞或詞的開頭（手＋ㄓㄡˇ 是手肘）。
func (s *Session) readingContinues(done zhuyin.Syllable) bool {
	if p, ok := s.prefixLiteralBefore(); ok { // 和原樣留下的詞頭注音成詞（#107）
		seq := []zhuyin.Syllable{p.syllable(), done}
		if len(s.model.Source().Exact(seq)) > 0 || s.model.Source().HasPrefix(seq) {
			return true
		}
	}
	begin := s.cursor
	for begin > 0 && !s.literals[begin-1] {
		begin--
	}
	begin = max(begin, s.cursor-decode.MaxWordLen+1)
	for at := begin; at < s.cursor; at++ {
		seq := append(append([]zhuyin.Syllable{}, s.syls[at:s.cursor]...), done)
		if len(s.model.Source().Exact(seq)) > 0 || s.model.Source().HasPrefix(seq) {
			return true
		}
	}
	return false
}

// neutralFallback（#69）：句尾語氣詞常打成輕聲，詞庫卻只收一聲，例如 ㄛ˙ 沒有字、
// ㄛ 是「喔」。輕聲讀音在目前語境完全找不到時改用一聲；有字或一聲也沒有時維持原樣。
func (s *Session) neutralFallback(done zhuyin.Syllable) zhuyin.Syllable {
	if done.ToneOf() != 5 || s.readingReachable(done) {
		return done
	}
	if first := done.WithTone(1); s.readingReachable(first) {
		return first
	}
	return done
}

func (s *Session) finishMixedTone(key string) bool {
	sym, ok := s.layout.Symbol([]rune(key)[0])
	if !ok || zhuyin.ClassOf(sym) != zhuyin.Tone || s.mixed.choice == 1 || s.mixed.accepted != "" {
		return false
	}
	raw := s.mixed.raw
	// 使用者加的英文詞接空白照字面送出（ls␣ 不是「孬」）；英文詞裡的聲調鍵（ipv6 的 6）打到一半時也不當聲調。
	// 英文詞以外的聲調鍵（ls3）照舊組成中文，↓ 選了注音解釋後空白也照舊完成一聲。
	if s.mixed.choice != 2 && (key == " " && s.vocabEnglish(raw) || key != " " && s.vocabEnglishPrefix(raw+key)) {
		return false
	}
	// Keep the one-letter English word a. Other collisions, including i
	// (ㄛ 喔, #69), let a reachable Chinese reading win the tone; uppercase I is
	// not a phonetic key and stays English. Literal selection above opts out.
	if key == " " && mixedLetterWord(raw) && s.mixed.choice != 2 && s.mixedEnglishWord(raw) {
		return false
	}
	if key == " " && s.mixed.choice != 2 {
		if at, done, ok := s.mixedPunctSplit(raw, sym); ok {
			prefix, rest := raw[:at], raw[at:]
			s.clearMixed()
			s.insertMixedLiteral(prefix, false, false)
			s.insertMixedTyped(done, rest+key)
			return true
		}
		if s.mixedSpaceNumber(raw, sym) {
			s.flushMixed()
			s.insertMixedLiteral(" ", false, false)
			return true
		}
	}
	if key != " " && s.mixed.choice == 0 && (s.digitToneNumber(raw, sym) || s.numberAfterLiteral(raw, key) ||
		s.rareDigitReading(raw, sym)) {
		return false
	}
	// #119：緊接中文的完整反序音節（摺＋u2,6 → 摺疊）不拆成英文 u＋數字 2,6。
	// 句首、空白或英文後仍保留 #200 的數字寫法；也不放寬後面的英→中切分。
	englishNumber := key != " " && s.mixed.choice == 0 && s.englishThenNumber(raw, key)
	if englishNumber && !s.mixedAfterChinese() {
		return false
	}
	if done, ok := s.mixedReading(raw, sym); ok {
		s.clearMixed()
		s.insertMixedTyped(done, raw+key)
		return true
	}
	if englishNumber {
		return false
	}
	// A tone can resolve an unseparated English→Chinese boundary: helloji3.
	// A complete English word keeps its space instead (#59: hell+o → ㄟ), and
	// its digit too (#119: hello4 is not hell欸, zoom3 is not zoo與).
	if s.mixed.choice == 0 && s.mixedEnglishWord(raw) {
		return false
	}
	if at, done, ok := s.mixedBoundary(raw, key, sym); ok {
		prefix := raw[:at]
		s.clearMixed()
		s.insertMixedLiteral(prefix, false, false)
		s.insertMixedTyped(done, raw[at:]+key)
		return true
	}
	// 畫面顯示成注音、空白組不成字（單獨的聲母 c ㄏ、x ㄌ，#190），或 ↓ 選了注音解釋後的聲調組不成字（2 ↓ㄉ，#208）：
	// 比照一般注音模式（#109）原樣留下注音，空白當一聲吃掉，不另外插入空白（c␣c␣ 是「ㄏㄏ」，不是「ㄏ ㄏ 」）。
	// 結尾是聲調鍵、照字面顯示的按鍵（co6）照舊。
	if key == " " || s.mixed.choice == 2 {
		return s.keepMixedSyllable(sym, raw+key)
	}
	return false
}

// keepMixedSyllable 把畫面顯示成注音的待定按鍵加上 tone 原樣留成注音（insertSyllableLiteral），記下原按鍵 keys：
// 在留下的注音上按 ↓，第二項是原按鍵（c␣，比照 #162 的 20␣），選了變回英數。周圍已顯示的字照 flushMixed 保留。
// 待定按鍵沒有顯示成注音時不動，回傳 false。
func (s *Session) keepMixedSyllable(tone rune, keys string) bool {
	c, ok := s.mixedPending(s.mixedText())
	if !ok {
		return false
	}
	before := c.Clone()
	done, ok := c.Add(tone)
	if !ok {
		return false
	}
	previous, at, n := slices.Clone(s.path), s.cursor, len(s.syls)
	s.clearMixed()
	s.insertSyllableLiteral(before, done)
	if s.mixedKeys == nil {
		s.mixedKeys = map[int]string{}
	}
	s.mixedKeys[at] = keys
	s.refresh()
	s.keepMixedDisplay(previous, at, len(s.syls)-n)
	return true
}

// 英→中邊界有兩種以上切法時，從最長的已知英文前段開始，把英文字尾的按鍵讓給中文音節，要讓後的音節
// 解碼機率至少是原來的這個倍數才讓（#119）。英文字尾的 s（ㄋ）門檻低：中英夾雜時英文很少加複數或第三人稱的 s，
// ㄋ 卻是常見的聲母，兩邊差不多常用時取 hello你、test你，不是 hellos以、tests以；短的音節常用得多時照舊
// （tests有、apps要，不是 test紐、app尿）。其他字母（user、server 的 r 是 ㄐ）要中文明顯常用得多才讓，
// 免得 user應 變成 use競；數字與標點鍵差距通常很大（api2呃 → api的）。
const (
	mixedPluralRatio = 0.25
	mixedMoveRatio   = 10
)

// mixedUnknownWordFloor：字典不認得的前段（ok、gmail、iphone）只在後段組成常用字時切開，
// 帳號、型號裡碰巧組得成音節的罕用字（kevin87 的 ㄙㄚ˙）照字面。量級約「嗎」「打」的一半。
// 前段不在字典裡就沒有英文的證據，音節照使用者打的長度取：兩邊差不到一半時取較長的音節（ok會，不是 okc為），
// 短的常用得多時取短的（netflix有，不是 netfli柳）。
const (
	mixedUnknownWordFloor = 1e-4
	mixedUnknownRatio     = 0.5
)

// mixedBoundary（#119）：聲調鍵前的待定按鍵整串組不成音節時，找英→中的切點。前段照 mixedLiteralPrefix
// 是已知英文、數字或標點的切法全部列出來，依 mixedPluralRatio／mixedMoveRatio 比較後段音節的解碼機率，
// 不再固定取最長的英文前段：hellosu3 → hello你（hellos 也是英文字）、api2k7 → api的。都切不開時，字典不認得的
// 英文前段（ok、gmail、iPhone）在明確的聲調鍵、後段至少兩個照順序打的注音鍵且組成常用字時也切開；
// 接空白、前段夾數字或標點（網址、識別字）、聲調前一鍵是數字（帳號、編號）都照字面。整串本身是英文字（step3、zoom3）在呼叫前就排除。
func (s *Session) mixedBoundary(raw, key string, tone rune) (int, zhuyin.Syllable, bool) {
	if key == " " && s.englishThenPunct(raw) {
		return 0, "", false
	}
	best, bestScore, bestSyl := 0, 0.0, zhuyin.Syllable("")
	for at := len(raw) - 1; at >= 1; at-- {
		// 後段的按鍵要照聲母、介音、韻母的順序（#176：hello13 的 o1 是 ㄟㄅ，不是 hell北）。真的在打注音時照順序打，
		// 把英文字尾借去組字時常顛倒；跟 #175 英文後面的反序數字留數字一樣。
		if !s.mixedLiteralPrefix(raw[:at], raw[at:], key) || !s.keysInSyllableOrder(raw[at:]) || s.rareDigitReading(raw[at:], tone) {
			continue
		}
		done, ok := s.mixedReading(raw[at:], tone)
		if !ok {
			continue
		}
		score := s.mixedSyllableScore(done)
		ratio := float64(mixedMoveRatio)
		if best > 0 && raw[at:best] == "s" && s.mixedEnglishWord(raw[:at]) {
			ratio = mixedPluralRatio
		}
		if best == 0 || score > 0 && score >= ratio*bestScore {
			best, bestScore, bestSyl = at, score, done
		}
	}
	if best > 0 {
		return best, bestSyl, true
	}
	// 打錯一鍵的注音（#203）：後段以數字鍵結尾（qji3wu0 的 ㄉㄧㄢ 天）也要切開，但切出來的後段要有字母，
	// 只用數字鍵的後段照舊交給下面「帳號、編號」的判斷。接空白的單一鍵只認聲母（ㄗ 資、ㄙ 思）：型號、版本後面的
	// 單一字母（gpt4o、ps4u、win7u）不是 ㄟ、ㄧ 一聲。
	if at, done, ok := s.mixedAfterTypo(raw, tone); ok && (!unicode.IsDigit(rune(raw[len(raw)-1])) || strings.ContainsFunc(raw[at:], unicode.IsLetter)) &&
		(key != " " || len(raw)-at >= 2 || s.keyIsInitial(rune(raw[at]))) {
		return at, done, true
	}
	// 聲調前一鍵是數字（lisa03、nina87）像帳號或編號，不切。
	if unicode.IsDigit(rune(raw[len(raw)-1])) {
		return 0, "", false
	}
	// 字典不認得的前段接空白，照字面。
	if key == " " {
		return 0, "", false
	}
	for at := len(raw) - 2; at >= 2; at-- {
		if strings.ContainsFunc(raw[:at], func(r rune) bool { return r > unicode.MaxASCII || !unicode.IsLetter(r) }) ||
			!s.keysInSyllableOrder(raw[at:]) {
			continue
		}
		done, ok := s.mixedReading(raw[at:], tone)
		if !ok || tone == '˙' && done.ToneOf() != 5 { // 輕聲找不到字改讀一聲的不算（kubectl7 不是 kubec超）
			continue
		}
		if score := s.mixedSyllableScore(done); score >= mixedUnknownWordFloor && (best == 0 || score >= mixedUnknownRatio*bestScore) {
			best, bestScore, bestSyl = at, score, done
		}
	}
	return best, bestSyl, best > 0
}

// mixedAfterTypo：打錯一鍵的注音（qji3：ㄆㄨㄛˇ 組不成音節）在聲調鍵後仍是待定的按鍵，之後的注音會一路接在後面，
// 直到下一個空白才整串照字面送出，好幾個字都變成英文字母（qji3ap6rup 而不是 qji3們今）。前段最後一個聲調鍵
// （前一鍵不是數字：帳號、編號照舊）之後的按鍵照順序打、組成的不是罕用字（mixedRareFloor）時，從那裡切開：打錯的那段照字面，
// 後面照常是中文。前段要有字母：只有數字與標點的是數字寫法。罕用字門檻不用 mixedUnknownWordFloor 的常用字門檻：
// 句首機率會隨個人用字大幅變動（整串照字面送出的句子讓個人模型學到「句首接天」，們的句首機率掉到 5e-5）。
func (s *Session) mixedAfterTypo(raw string, tone rune) (int, zhuyin.Syllable, bool) {
	at := strings.LastIndexFunc(raw, func(r rune) bool {
		sym, ok := s.layout.Symbol(r)
		return ok && r != ' ' && zhuyin.ClassOf(sym) == zhuyin.Tone
	}) + 1
	if at < 2 || at >= len(raw) || unicode.IsDigit(rune(raw[at-2])) || !s.keysInSyllableOrder(raw[at:]) {
		return 0, "", false
	}
	if strings.ContainsFunc(raw[:at], func(r rune) bool { _, ok := s.layout.Symbol(r); return !ok }) {
		return 0, "", false // 大寫、底線等不是注音鍵的字元：識別字或英文，不是打錯的注音
	}
	if !strings.ContainsFunc(raw[:at], unicode.IsLetter) {
		return 0, "", false // 只有數字與標點的前段是數字寫法（1-4-3、2025.3.31、2.7-3.1），不是打錯的注音
	}
	done, ok := s.mixedReading(raw[at:], tone)
	if !ok || tone == '˙' && done.ToneOf() != 5 || s.mixedSyllableScore(done) < mixedRareFloor {
		return 0, "", false
	}
	return at, done, true
}

// keyIsInitial：這個鍵在目前的鍵盤排列是聲母。
func (s *Session) keyIsInitial(r rune) bool {
	sym, ok := s.layout.Symbol(r)
	return ok && zhuyin.ClassOf(sym) == zhuyin.Initial
}

// englishThenPunct（#188）：英文字後面緊接著只有標點鍵（hello. 、vs. 、hello, 、hello; ），比照 #187「緊接在半形英數後面的
// . , 是標點」，接空白時整串照字面，不把標點鍵切給中文（hello歐、vs歐），也不改把英文字尾借去組字（max. 不是 ma摟）。
// 有數字（app2. 都、hello5. 周）或字母（helloej/ 公）的後段照舊由 mixedBoundary 判斷；明確的聲調鍵（hello.3）不在此限。
func (s *Session) englishThenPunct(raw string) bool {
	word := strings.TrimRightFunc(raw, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return word != raw && word != "" && s.mixedEnglishWord(word)
}

// englishThenNumber（#200）：英文字後面緊接著「數字＋. : ,＋數字」的數字寫法（hello2.4、hello5.6、gpt2.5），整串照字面，
// 跟 englishThenPunct 一樣不把英文後面的按鍵切給中文（hello豆、hello軸）；常見的是版本、型號與財報數字（eps2.5、CPI1.2）。
// 英文字不必在字典裡。/ 不比照：英文後面緊接著打的「等」「整」「正」（Google等、AI整、IP正）都是 數字鍵＋/＋聲調鍵，
// PTT 語料 226 次，比照的話有 73 次變成數字，英文緊接的分數卻一次也沒有。英文後面先打空白（hello 2.4 → hello 豆，
// 跟單獨打的 2.4 一樣）、只有數字加標點再接空白（hello5. → hello周，#188）照舊。
func (s *Session) englishThenNumber(raw, key string) bool {
	word := strings.TrimRightFunc(raw, func(r rune) bool { return !unicode.IsLetter(r) })
	tail := raw[len(word):] + key
	return word != "" && tail != key && strings.ContainsAny(tail, ".,:") && !strings.ContainsAny(tail, "/") && numberLike(tail) &&
		!strings.ContainsFunc(word, func(r rune) bool { return r > unicode.MaxASCII })
}

// mixedRareFloor（#200）：只用數字鍵與標點鍵拼出的讀音，單音節機率（mixedSyllableScore）低於這個值就照字面。
// 0.2.4 資料包裡，低於的是 1/3 繃、1/6 甭、5.3 肘、2.6 艔、86 嗄、06 玵、197 唄、287 瘩、.4 嘔、,4 誒、.6 吽、;3 軮
// （PTT 語料合計 4 次）；最接近的常用一邊是 583 眨（2.2e-6）、03 俺（2.6e-6）、5.4 咒／宙（5.7e-6），2.4 豆、5.6 軸、
// 1/4 蹦都在 1e-5 左右或更高（門檻的 6 倍以上）。
const mixedRareFloor = 1.5e-6

// rareDigitReading（#200）：沒有字母的待定按鍵加上明確的聲調鍵（3 4 6 7）組成的讀音，首選字是罕用字時照字面
// （1/3 不是「繃」、86 不是「嗄」），比照常見注音輸入法混打「組不出常用字就照字面」，跟 #175 的 57、#176 的 2.7 一樣
// ↓ 仍有帶聲調的注音。跟前面的中文成詞（手＋5.3 是手肘）時照舊出字；個人用過的字機率會變高，也照舊出字。
func (s *Session) rareDigitReading(raw string, tone rune) bool {
	if tone == zhuyin.Tone1 || raw == "" || strings.ContainsFunc(raw, unicode.IsLetter) {
		return false
	}
	done, ok := s.mixedReading(raw, tone)
	return ok && s.mixedSyllableScore(done) < mixedRareFloor && !s.readingContinues(done)
}

// mixedSyllableScore：單一音節在句首的解碼機率（含個人用字）。只在多字詞裡常見的音節以詞首計（應 → 應該）。
func (s *Session) mixedSyllableScore(syl zhuyin.Syllable) float64 {
	dec := &decode.Decoder{Lex: s.model.Source(), Score: s.model}
	_, score := dec.Best(input.Exact{}.Expand([]zhuyin.Syllable{syl}), nil, true)
	return math.Exp(score)
}

// keysInSyllableOrder：按鍵依聲母、介音、韻母的順序打（su 是 ㄋㄧ）。真的在打注音時是照順序打的；
// 英文字尾的字母被借去組音節時通常順序顛倒（facebook＋g 的 kg 是 ㄜㄕ），不在字典裡的前段不這樣切。
func (s *Session) keysInSyllableOrder(keys string) bool {
	last := zhuyin.None
	for _, k := range keys {
		sym, _ := s.layout.Symbol(k)
		c := zhuyin.ClassOf(sym)
		if c <= last {
			return false
		}
		last = c
	}
	return true
}

// mixedLiteralPrefix 判斷聲調前的按鍵能否在 at 切開：前段留原樣、後段組成中文。前段可以是英文單字，
// 也可以是數字或標點（2024ru04 → 2024建，,ji3 → ,我；標準排列的 , . / ; - 與 1 2 5 8 9 0 都是注音鍵），
// 或英文單字接數字／標點（iphone15ru04）。前段以數字或標點結尾時，後段必須有字母，
// 否則 154、19 8 這類數字會被拆成「1這」「19啊」；也必須是明確的聲調鍵（3 4 6 7），一聲的空白不拆，
// 否則 5pm、5g、1080p 會變成「5暈」「5詩」「1080恩」（#78 後續）。
func (s *Session) mixedLiteralPrefix(prefix, rest, key string) bool {
	word := strings.TrimRightFunc(prefix, func(r rune) bool { return !unicode.IsLetter(r) })
	if word == prefix {
		return s.mixedEnglishWord(prefix)
	}
	if key == " " || !strings.ContainsFunc(rest, unicode.IsLetter) {
		return false
	}
	return word == "" && !strings.ContainsFunc(prefix, unicode.IsLetter) || s.mixedEnglishWord(word)
}

// mixedPunctSplit（#116）：標點後面緊接著讀成中文的數字按鍵（我,2. → 我,都；,2; → ,當）接空白時，標點照字面、
// 後段組成中文；回傳切點與後段的音節。標點也是注音鍵（, 是 ㄝ），整串組不成音節，不切開就會整串照字面留下。
func (s *Session) mixedPunctSplit(raw string, tone rune) (int, zhuyin.Syllable, bool) {
	for at := 1; at < len(raw); at++ {
		prefix, rest := raw[:at], raw[at:]
		if strings.ContainsFunc(prefix, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			break
		}
		if strings.ContainsFunc(rest, unicode.IsLetter) || !strings.ContainsFunc(rest, unicode.IsDigit) ||
			numberLike(rest) && !strings.HasSuffix(rest, ".") {
			continue
		}
		if done, ok := s.mixedReading(rest, tone); ok {
			return at, done, true
		}
	}
	return 0, "", false
}

// mixedSpaceNumber：只有數字與標點的待定按鍵接空白時是數字。前一個字是中文才當一聲（「我們」後打 10 空白 → 班）；在開頭，
// 或接在空白、英文、數字後面是數字（「iPhone 15」「2026 」），與畫面照字面顯示一致（#78 後續）。
// 要中文可按 ↓ 選注音解釋。只用數字鍵就拼得成一聲字的按鍵（20 單、18 八、5 之）不論位置都出中文（digitWord，#162），
// 句首或空白、全形標點後單獨的 . ㄡ、, ㄝ 也是（punctKeyWord，#187：. 歐），要數字或標點按 Enter。
// 不是數字寫法、一聲又組得成字的數字按鍵也照一般一聲組成中文（digitsReadAsChinese）：句首單打「當」「都」「周」。
// 單一個數字鍵出的字（5 之）是中文，後面的數字照接在中文後面處理，不再把那個字改回原按鍵（#162：要數字一律按 Enter）。
func (s *Session) mixedSpaceNumber(raw string, tone rune) bool {
	return !strings.ContainsFunc(raw, unicode.IsLetter) && !s.mixedAfterChinese() &&
		!s.digitsReadAsChinese(raw, tone) && !s.digitWord(raw, tone)
}

// mixedSpaceReading（#162）：中英混打時照字面顯示成數字的待定按鍵（有數字、沒有字母），按空白會組成中文時，
// 回傳組成中文的那段按鍵的起點與讀音（20 → 0、ㄉㄢ；我,2. 的 ,2. → 1、ㄉㄡ），供快照的讀音提示。
// 判斷與 finishMixedTone 接空白的順序一致：標點切開、數字、一聲組成中文。拼不成音節（2026）、
// 數字寫法（3.5、318）、已用 ↓ 選過原按鍵或注音解釋、候選開著時沒有提示。
func (s *Session) mixedSpaceReading() (int, zhuyin.Syllable, bool) {
	raw := s.mixed.raw
	if raw == "" || s.english || s.englishWord || s.cand != nil || s.mixed.choice != 0 || s.mixed.accepted != "" ||
		strings.ContainsFunc(raw, unicode.IsLetter) || !strings.ContainsFunc(raw, unicode.IsDigit) && !s.punctKeyWord(raw) ||
		s.vocabEnglish(raw) || s.mixedText() != raw {
		return 0, "", false
	}
	if at, done, ok := s.mixedPunctSplit(raw, zhuyin.Tone1); ok {
		return at, done, true
	}
	if s.mixedSpaceNumber(raw, zhuyin.Tone1) {
		return 0, "", false
	}
	done, ok := s.mixedReading(raw, zhuyin.Tone1)
	return 0, done, ok
}

// digitWord（#162）：只用數字鍵、照聲母韻母的順序打完，一聲自己就是字的按鍵（標準排列的 20 ㄉㄢ 單、18 ㄅㄚ 八、
// 10 ㄅㄢ 班、5 ㄓ 之、8 ㄚ 啊）接空白，不論在句首或接在中文、空白、英文、數字後面，都照一般一聲出字，字由解碼依
// 前後文決定；要數字按 Enter（2 0 Enter 送出 20），出字後 ↓ 第二個候選是原按鍵。看的是字典裡有沒有這個單音節的字，
// 不是固定的鍵表：1 ㄅ、2 ㄉ 自己組不成字，照舊是數字；拼不成音節的（2026、100、12、15）也是數字。
// 按鍵順序顛倒（05、81，日期與編號常見）不算；前面緊接著字面數字或數字標點時（318 的 18：3 是聲調鍵先照字面送出，
// 3.18 的 .18）是更長的數字的一部分，照舊。標點鍵只有 punctKeyWord 的 . ㄡ、, ㄝ 比照辦理。
func (s *Session) digitWord(raw string, tone rune) bool {
	if tone != zhuyin.Tone1 || raw == "" || strings.Trim(raw, "0123456789") != "" && !s.punctKeyWord(raw) ||
		!s.keysInSyllableOrder(raw) || s.literalNumberBefore() != "" {
		return false
	}
	done, ok := s.mixedReading(raw, tone)
	return ok && done.ToneOf() == 1 && len(s.model.Source().Exact([]zhuyin.Syllable{done})) > 0
}

// punctKeyWord（#187）：標準排列單獨一個 . ㄡ 或 , ㄝ，在句首或接在空白、中文、全形標點後面時，跟 digitWord 的數字鍵
// 一樣接空白出一聲的字（. 歐，. 5. tk 是歐洲車），要標點按 Enter，出字後 ↓ 第二個候選是原按鍵。緊接在字面的英文字、
// 數字或半形標點後面（Tab 補完的 hello 後打的句點、3. 1, 的編號）照舊是標點。其他標點鍵不比照：; ㄤ（骯）、/ ㄥ（鞥）是罕用字，
// 「A / B」「a ; b」與程式碼常見；- ㄦ 是條列（「- 我」）。字典裡沒有這個一聲字時（, 在內附字典沒有 ㄝ）照舊是標點。
func (s *Session) punctKeyWord(raw string) bool {
	if raw != "." && raw != "," {
		return false
	}
	if s.cursor == 0 || !s.literals[s.cursor-1] || s.syls[s.cursor-1] == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(string(s.syls[s.cursor-1]))
	return last > unicode.MaxASCII || unicode.IsSpace(last)
}

// digitToneNumber（#175，方案 b）：只用數字鍵、最後一鍵是聲調鍵（3 4 6 7）的待定按鍵，在下面三種情況照字面
// 留成數字，不組成中文：
//   - 帶調讀音在詞庫沒有字，要靠 #69 的輕聲退回一聲才組得成（57 ㄓ˙→之、107 ㄅㄢ˙→班、07 ㄢ˙→安）；
//   - 在句首或接在空白、英文、標點後面，按鍵順序顛倒（813 的 ㄚㄅ、017 的 ㄢㄅ），真的在打注音時不會這樣打；
//   - 前面緊接著字面數字或數字標點（354 的 3 已照字面送出），是更長的數字的一部分。
//
// 第一種也適用於夾著標點鍵的數字（#176：2.7 的 ㄉㄡ˙ 沒有字，不退回一聲出「都」）。
//
// 其餘（53 只、54 至、84 阿、104 辦）照舊，跟常見注音輸入法的混打一樣按下聲調就出字。緊接在中文後面的
// 反序按鍵也出字（#190：大概這麼 824 → 大、中英混 823 → 打）：一般注音模式與接空白的一聲本來就不看順序，
// 中文後面緊接著打、又會這樣組成反序音節的數字很少（PTT 語料 24,383 次中 23 次）；要數字按 ↓ 選第二項的原按鍵。
func (s *Session) digitToneNumber(raw string, tone rune) bool {
	if raw == "" || strings.ContainsFunc(raw, unicode.IsLetter) || !strings.ContainsFunc(raw, unicode.IsDigit) {
		return false
	}
	digits := strings.Trim(raw, "0123456789") == ""
	if digits && (s.literalNumberBefore() != "" || !s.keysInSyllableOrder(raw) && !s.mixedAfterChinese()) {
		return true
	}
	c, ok := s.mixedComposer(raw)
	if !ok {
		return false
	}
	done, ok := c.Add(tone)
	if !ok {
		return false
	}
	return done.ToneOf() == 5 && !s.readingReachable(done)
}

// numberAfterLiteral（#176）：緊接在字面數字後面、沒有字母的待定按鍵，加上這個聲調鍵照字面是數字寫法時
// （3.4 的 .4、3-4 的 -4、6/23 的 /2＋3、3,472 的 ,4），照字面留下，不把標點鍵和聲調組成中文（3嘔、3二、6等、3誒72）。
// 聲調鍵 3 4 6 7 沒在組字時先照字面送出，3.4 的待定按鍵只剩 .4，要連同前面的 3 一起看；全是數字的（354）由 digitToneNumber 處理。
// 前面不是字面數字（句首、中文、空白後）的 2.4 豆、2/3 等、5/4 正照舊組成中文。
func (s *Session) numberAfterLiteral(raw, key string) bool {
	lit := s.literalNumberBefore()
	return lit != "" && !strings.ContainsFunc(raw, unicode.IsLetter) && numberLike(lit+raw+key)
}

// reviseDigitWord（#176）：數字鍵＋聲調鍵出的字（#175 的 03 俺、04 案、53 只、84 阿、103 版），可能隔著照字面送出的數字
// （846,000 的 6），後面緊接著一個數字標點再打數字時（03:00、04:10、53,082、846,000、04-06），是時間或數字寫法：
// 把那個字改回原按鍵，後面的按鍵照數字寫法接上（: 在字後面已打成全形「：」，改回半形）。比照 #69 的 i 回頭改成英文。
// 沒有選過字（鎖定）才改；標點鍵和這個聲調鍵組得成有字的音節時是中文（294-3 戴爾、284-6 大而），也不改。
// 不是數字的（「只：」「案」後面接中文）照舊。那個字緊接在中文後面時（#201）只在它自己緊接著半形 . , /
// 再打數字時改回（我53,082、毛利率54.88、莫忘04/09）：冒號是「答案：1」，- 是負數（三大-800、跌至-13），
// 中間隔著照字面送出的數字是那個字本身加上數字（高達3.2、降至6,762），都照舊。
func (s *Session) reviseDigitWord(k string) {
	if len(k) != 1 || k[0] < '0' || k[0] > '9' || s.mixed.choice != 0 || s.mixed.accepted != "" {
		return
	}
	end, raw := s.cursor, s.mixed.raw
	colon := raw == "" && end > 0 && s.literals[end-1] && string(s.syls[end-1]) == "："
	switch {
	case colon:
		end--
	case len(raw) != 1 || !strings.Contains(".,/-", raw):
		return
	default:
		if sym, ok := s.layout.Symbol(rune(k[0])); ok && zhuyin.ClassOf(sym) == zhuyin.Tone {
			if c, ok := s.mixedComposer(raw); ok {
				if done, ok := c.Add(sym); ok && s.readingReachable(done) && !s.rareDigitReading(raw, sym) {
					return
				}
			}
		}
	}
	at := end - 1
	for at >= 0 && s.literals[at] && len(s.syls[at]) == 1 && s.syls[at][0] >= '0' && s.syls[at][0] <= '9' {
		at--
	}
	keys := s.mixedKeys[at]
	afterChinese := at > 0 && !s.literals[at-1]
	if at < 0 || s.literals[at] || afterChinese && (colon || raw == "-" || at < end-1) || len(keys) < 2 ||
		strings.Trim(keys, "0123456789") != "" || !strings.ContainsAny(keys[len(keys)-1:], "3467") {
		return
	}
	for start, lock := range s.locks {
		if start <= at && at < start+lock.Len {
			return
		}
	}
	text := keys
	for _, syl := range s.syls[at+1 : end] {
		text += string(syl)
	}
	for s.cursor > at {
		s.eraseBeforeCursor()
	}
	if colon {
		s.mixed.raw = ":"
	}
	s.insertMixedLiteral(text, false, false)
	s.refresh()
}

// literalDigitBefore：游標前一個字是照字面送出的半形數字。
func (s *Session) literalDigitBefore() bool {
	at := s.cursor - 1
	return at >= 0 && s.literals[at] && len(s.syls[at]) == 1 && s.syls[at][0] >= '0' && s.syls[at][0] <= '9'
}

// digitWordKeys：at 是只用數字鍵加空白打出的中文字（#134 的 5 之、#162 的 20 單），或只用數字鍵加聲調鍵打出的字
// （#175：54 至、53 只），或單獨一個 . , 加空白打出的字（#187：. 歐），↓ 的原按鍵排第二個。
func (s *Session) digitWordKeys(at int) bool {
	if at < 0 || at >= len(s.syls) || s.literals[at] {
		return false
	}
	keys := s.mixedKeys[at]
	digits, ok := strings.CutSuffix(keys, " ")
	if !ok && len(keys) > 1 && strings.ContainsAny(keys[len(keys)-1:], "3467") {
		digits, ok = keys, true
	}
	return ok && digits != "" && (strings.Trim(digits, "0123456789") == "" || digits == "." || digits == ",")
}

// digitsReadAsChinese（#89、#114、#116）：含數字的待定按鍵接空白時，一聲組得成字，而且照字面不是數字寫法
// （2; 1; 5/ 夾著不會出現在數字裡的標點），或結尾是 .（2. 都、5. 周）時讀成中文。條列編號的「1. 」「10. 」
// 組不成音節，照舊是數字；真的要「2. 」可按 ↓ 選原按鍵。小數、時間、分數（3.5、10:30、5/8）是數字寫法，不受影響。
func (s *Session) digitsReadAsChinese(raw string, tone rune) bool {
	if !strings.ContainsFunc(raw, unicode.IsDigit) {
		return false
	}
	if _, ok := s.mixedReading(raw, tone); !ok {
		return false
	}
	full := s.literalNumberBefore() + raw
	return !numberLike(full) || strings.HasSuffix(full, ".")
}

// numberLike：按鍵照字面看是數字寫法：數字之間可夾 . , : / -（3.5、1,000、10:30、5/8、2-3），
// 結尾可帶 . 或 ,（條列的「1. 」、列舉的「1, 2」）。; 不會出現在數字裡，結尾的 / - 也不是數字。
func numberLike(raw string) bool {
	digit := false
	for i, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case strings.ContainsRune(".,:/-", r) && digit:
			last := i == len(raw)-1
			if last && r != '.' && r != ',' || !last && (raw[i+1] < '0' || raw[i+1] > '9') {
				return false
			}
			digit = false
		default:
			return false
		}
	}
	return raw != ""
}

// literalNumberBefore：游標前面緊接著的字面數字與數字標點。聲調鍵 3 4 6 7 先照字面送出，
// 3.5 的待定按鍵只剩 .5，要連同前面的 3 一起判斷是不是數字寫法。
func (s *Session) literalNumberBefore() string {
	at := s.cursor
	for at > 0 && s.literals[at-1] && len(s.syls[at-1]) == 1 && strings.Contains("0123456789.,:/-", string(s.syls[at-1])) {
		at--
	}
	var b strings.Builder
	for _, syl := range s.syls[at:s.cursor] {
		b.WriteString(string(syl))
	}
	return b.String()
}

// mixedReachable：待定按鍵加上某個聲調可以組成詞庫裡的音節。
func (s *Session) mixedReachable(raw string) bool {
	for _, tone := range []rune{zhuyin.Tone1, 'ˊ', 'ˇ', 'ˋ', '˙'} {
		if _, ok := s.mixedReading(raw, tone); ok {
			return true
		}
	}
	return false
}

// mixedAfterChinese：待定按鍵要插入的位置前面是中文音節（不是字面的英文、數字、空白或標點）。
func (s *Session) mixedAfterChinese() bool {
	if _, ok := s.prefixLiteralBefore(); ok {
		return true // 只當詞頭而原樣留下的注音（#107）仍是中文，下一個音節可能和它成詞。
	}
	return s.cursor > 0 && !s.literals[s.cursor-1]
}

// mixedLetterWord is a one-key English word that keeps its space over a
// first-tone reading. Lowercase i is ㄛ (喔) far more often than the pronoun,
// which is normally typed as uppercase I (#69).
func mixedLetterWord(raw string) bool { return strings.EqualFold(raw, "a") }

// A full speller also recognizes letter names (t/u/g). They must not turn
// ordinary one-key first-tone Chinese into English; a and I are actual words.
func (s *Session) mixedEnglishWord(raw string) bool {
	return (len(raw) > 1 || strings.EqualFold(raw, "a") || strings.EqualFold(raw, "i")) && (s.English.Has(raw) || s.personalEnglishAllowed() && s.model.EnglishKnown(raw))
}

// mixedKey only handles printable pending input. Navigation/commit/candidates
// remain in the normal session state machine and flush through explicit hooks.
func (s *Session) mixedKey(k string) bool {
	if !s.mixedInput() || s.english {
		return false
	}
	if s.mixed.raw == "" {
		s.restorePendingLiteral()
	}
	if !s.comp.Empty() {
		return false
	}
	rs := []rune(k)
	if len(rs) == 1 && rs[0] > unicode.MaxASCII && unicode.IsPrint(rs[0]) {
		s.flushMixed()
		return false
	}
	if len(rs) != 1 || rs[0] > unicode.MaxASCII || !unicode.IsPrint(rs[0]) || k == "`" {
		return false
	}
	if k == " " && s.mixed.raw == "" {
		return false
	}
	if s.mixed.accepted != "" {
		s.flushMixed()
	}
	if len(s.mixed.raw) >= maxMixedPending {
		s.flushMixed()
	}
	// #208：↓ 選了注音解釋（2 → ㄉ、1 → ㄅ）之後接著打的鍵和它組不成同一個音節時（ㄉ 後面的 ㄊ），選好的注音先原樣留下，
	// 這一鍵開始新的待定按鍵；以前併成同一串、整串照字面顯示（我2wu0）。組得成的（ㄉ＋ㄜ）照舊接著組字，空白與聲調鍵由 finishMixedTone 完成。
	if s.mixed.choice == 2 && k != " " {
		if sym, ok := s.layout.Symbol(rs[0]); !ok || zhuyin.ClassOf(sym) != zhuyin.Tone {
			if _, ok := s.mixedComposer(s.mixed.raw + k); !ok {
				s.keepMixedSyllable(zhuyin.Tone1, s.mixed.raw)
			}
		}
	}
	s.reviseDigitWord(k)
	if s.mixed.raw != "" && s.finishMixedTone(k) {
		return true
	}
	if k == " " {
		s.flushMixed()
		s.insertMixedLiteral(" ", false, false)
		return true
	}
	if sym, ok := s.layout.Symbol(rs[0]); ok && zhuyin.ClassOf(sym) == zhuyin.Tone && s.mixed.raw == "" {
		s.insertMixedLiteral(k, false, false)
		return true
	}
	// 打到一半的英文裡 _ ^ 是識別字、算式的一部分（user_name、x^2），不當破折號／刪節號（#118）。
	if _, ok := s.layout.Punct(rs[0]); ok && !(s.mixed.raw != "" && strings.ContainsAny(k, "_^")) {
		if s.mixed.raw == "" {
			// 緊接在字面數字後面的 :（3:00、7:33，#176）跟 10:30 一樣是半形、併進待定按鍵；3 4 6 7 是聲調鍵，
			// 沒在組字時先照字面送出，: 來的時候待定按鍵是空的。
			if k == ":" && s.literalDigitBefore() {
				s.mixed.raw = k
				return true
			}
			return false
		}
		literal := s.mixedText() == s.mixed.raw
		// Colon/backslash belong to URLs and paths while an English token is pending.
		if strings.ContainsAny(k, ":\\'") && (literal || s.mixed.choice == 0 && s.mixedEnglishWord(s.mixed.raw)) {
			s.mixed.raw += k
			return true
		}
		s.flushMixed()
		// 緊接在照字面的英數後面，Shift＋, Shift＋. 也是「，」「。」（#201：我2026，我、我OK，），↓ 第二項是原按鍵 < >；
		// 其他標點照字面（hello! 、x^2）。
		if literal && k != "<" && k != ">" {
			s.insertMixedLiteral(k, false, false)
		} else {
			p, _ := s.layout.Punct(rs[0])
			s.insertPunctKey(k, p)
		}
		return true
	}
	s.mixed.raw += k
	return true
}

// vocabEnglish：raw 恰好是使用者加的英文詞（#146）。Preview02 為零值時不看自訂詞彙。
func (s *Session) vocabEnglish(raw string) bool {
	return s.Preview02 && s.model.VocabEnglish(raw)
}

func (s *Session) vocabEnglishPrefix(raw string) bool {
	return s.Preview02 && s.model.VocabEnglishPrefix(raw)
}

func (s *Session) personalEnglishAllowed() bool { return !s.private && !s.paused && !s.localBlocked }

func (s *Session) englishPrefix(prefix string, limit int) []english.Candidate {
	if !s.personalEnglishAllowed() {
		return s.English.Prefix(prefix, limit)
	}
	return s.model.EnglishPrefix(s.English, prefix, limit)
}

func (s *Session) englishCorrections(text string, limit int) []english.Candidate {
	if !s.personalEnglishAllowed() {
		return s.English.Corrections(text, limit)
	}
	return s.model.EnglishCorrections(s.English, text, limit)
}

type mixedOrigin struct{ assisted, explicit bool }

func (s *Session) insertMixedLiteral(text string, assisted, explicit bool) {
	at := s.cursor
	s.insertLiteral(text)
	if s.mixedOrigins == nil {
		s.mixedOrigins = map[int]mixedOrigin{}
	}
	for pos := at; pos < s.cursor; pos++ {
		s.mixedOrigins[pos] = mixedOrigin{assisted, explicit}
	}
}

func (s *Session) shiftMixedOrigins(at, delta int) {
	if len(s.mixedOrigins) == 0 {
		return
	}
	next := map[int]mixedOrigin{}
	for pos, origin := range s.mixedOrigins {
		switch {
		case pos < at:
			next[pos] = origin
		case delta > 0 || pos > at:
			next[pos+delta] = origin
		}
	}
	s.mixedOrigins = next
}

// Extract final literal tokens, not a history of transient predictions. Missing
// key origins (symbols/paste/manual English), paths and URLs cannot confirm a
// word. Edits move or remove origins together with their visible characters.
func (s *Session) mixedEnglishCommits() []personal.EnglishCommit {
	var out []personal.EnglishCommit
	for at := 0; at < len(s.syls); {
		if !s.literals[at] {
			at++
			continue
		}
		end := at
		for end < len(s.syls) && s.literals[end] {
			end++
		}
		start := at
		for pos := at; pos <= end; pos++ {
			if pos < end && !unicode.IsSpace([]rune(string(s.syls[pos]))[0]) {
				continue
			}
			left, right := start, pos
			// Deliberately exclude period/slash/colon to avoid learning file paths
			// or parts of a URL as standalone names.
			punct := func(i int) bool { return strings.ContainsAny(string(s.syls[i]), "\"'()[]{}<>,!?;") }
			for left < right && punct(left) {
				left++
			}
			for right > left && punct(right-1) {
				right--
			}
			var word strings.Builder
			event := personal.EnglishCommit{Explicit: true}
			tracked := left < right
			for i := left; i < right; i++ {
				word.WriteString(string(s.syls[i]))
				origin, ok := s.mixedOrigins[i]
				tracked = tracked && ok
				event.Assisted = event.Assisted || origin.assisted
				event.Explicit = event.Explicit && origin.explicit
			}
			event.Text = word.String()
			if tracked && personal.EnglishToken(event.Text) {
				event.Suspicious = s.English.NearCommon(event.Text, 50)
				out = append(out, event)
			}
			start = pos + 1
		}
		at = end
	}
	return out
}

func (s *Session) mixedSnapshot(snap *proto.Snapshot) {
	if s.mixed.raw == "" || s.cand != nil || s.english {
		return
	}
	if text := s.mixedSuggestion(); text != "" {
		start := snap.Cursor.Rune - len([]rune(s.mixedText()))
		snap.Suggestion = s.mixed.raw + " → " + text
		snap.SuggestionEdit = &proto.SuggestionEdit{Start: start, Length: len([]rune(s.mixedText())), Text: text}
	}
}
