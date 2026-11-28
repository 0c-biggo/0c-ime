package session

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/proto"
)

func englishLetter(k string) bool {
	return len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= 'A' && k[0] <= 'Z')
}

func (s *Session) englishCanStart() bool {
	return s.EnglishAutocomplete && s.english && !s.composing() && !s.englishTokenBlocked && !s.private && !s.paused
}

func englishBoundary(k string) bool {
	return strings.ContainsAny(k, " \t\n\r") || len([]rune(k)) == 1 && unicode.IsSpace([]rune(k)[0]) ||
		k == proto.KeyEnter || k == proto.KeyTab || k == proto.KeyBackTab || k == proto.KeyLeft || k == proto.KeyRight ||
		k == proto.KeyUp || k == proto.KeyDown || k == proto.KeyHome || k == proto.KeyEnd || k == proto.KeyPageUp || k == proto.KeyPageDown
}

// englishKey 處理英文模式自動補字的按鍵。沒有組字時打的字母開始一個單字，邊界鍵送出它再交給程式；
// 單按 Shift 切英文、前面還留著中文組字時，字母也開始一個單字，接在游標處（englishCompositionKey，#372）。
func (s *Session) englishKey(k string) (handled bool, commit string, active bool) {
	if !s.englishWord {
		if s.composing() {
			if englishLetter(k) && s.englishCanStartInComposition() {
				s.insertPendingLiteral()
				s.englishWord = true
				s.mixed = pendingKeys{raw: k}
				return true, "", true
			}
			// 同沒有組字時：數字、底線、網址標點讓接著打的字母不補字，到下一個空白或導覽鍵為止。
			// 候選開著時（#369 的 ↓）按鍵是選字，不算。
			if s.cand == nil {
				s.noteEnglishToken(k)
			}
			return false, "", false
		}
		if englishBoundary(k) {
			s.englishTokenBlocked = false
		}
		if s.englishCanStart() && englishLetter(k) {
			s.englishWord = true
			s.mixed = pendingKeys{raw: k}
			return true, "", true
		}
		if len([]rune(k)) == 1 && !englishBoundary(k) {
			s.englishTokenBlocked = true
		}
		return false, "", true
	}
	if len(s.syls) > 0 {
		return s.englishCompositionKey(k)
	}
	if !s.private && !s.paused {
		switch k {
		case proto.KeyTab:
			if s.acceptEnglish() {
				return true, "", true
			}
		case proto.KeyEscape:
			s.mixed.accepted = ""
			s.cand = nil
			s.englishDismissed = true
			return true, "", true
		case proto.KeyUp, proto.KeyDown:
			if s.cand != nil && s.cand.english {
				delta := 1
				if k == proto.KeyUp {
					delta = -1
				}
				s.moveHighlight(delta)
				return true, "", true
			}
		case proto.KeyBackspace:
			if s.mixed.accepted != "" {
				s.mixed.accepted = ""
			} else {
				s.mixed.raw = s.mixed.raw[:len(s.mixed.raw)-1]
				s.mixed.assistedPrefix = min(s.mixed.assistedPrefix, len(s.mixed.raw))
			}
			s.cand = nil
			s.englishDismissed = false
			if s.mixed.raw == "" {
				s.clearMixed()
				s.englishWord = false
			}
			return true, "", true
		}
		if englishLetter(k) {
			raw := s.mixedText()
			if len(raw) < english.MaxWordBytes {
				assistedPrefix := s.mixed.assistedPrefix
				if s.mixed.accepted != "" {
					assistedPrefix = len(raw)
				}
				s.mixed = pendingKeys{raw: raw + k, assistedPrefix: assistedPrefix}
				s.cand = nil
				s.englishDismissed = false
				return true, "", true
			}
		}
	}
	// Enter 只送出畫面上的英文（#235），同注音組字與中英混打：組字區有底線時按 Enter 是確認這段字；
	// 連 Enter 一起交給程式，終端機會直接執行命令、聊天室會直接送出。要執行或換行再按一次 Enter。
	// 一樣不替使用者接受反白的建議。
	if k == proto.KeyEnter {
		commit = s.commit()
		s.englishTokenBlocked = false
		return true, commit, true
	}
	// Deliver exactly the displayed word before letting the original key reach
	// the application. Space never selects the highlighted completion.
	// A URL, identifier or full buffer may continue in the host after this
	// prefix. Only a word boundary or ordinary enclosing punctuation confirms it.
	if !englishBoundary(k) && !(len(k) == 1 && strings.ContainsAny(k, "\"'()[]{}<>,!?;")) {
		s.localBlocked = true
	}
	s.englishSpaceCommit = k == " "
	commit = s.commit()
	s.englishSpaceCommit = false
	s.englishTokenBlocked = !englishBoundary(k)
	return false, commit, true
}

// englishCanStartInComposition：英文模式保留著中文組字時（單按 Shift 切英文，#90），這個字母開始一個補字的英文字（#372）。
// 游標左右是英文字母或數字時是在改一個已經打好的詞（test 中間、3d 後面），照舊按原樣插入；候選開著、私密欄位、暫停時不補字。
func (s *Session) englishCanStartInComposition() bool {
	return s.EnglishAutocomplete && s.english && len(s.syls) > 0 && s.mixed.raw == "" && !s.keysPending() && !s.symbolPrefix &&
		s.cand == nil && !s.englishTokenBlocked && !s.private && !s.paused && !s.latinAt(s.cursor-1) && !s.latinAt(s.cursor)
}

// latinAt：組字第 at 格是照字面的英文字母或數字（全形也算）。
func (s *Session) latinAt(at int) bool {
	if at < 0 || at >= len(s.syls) || !s.literals[at] {
		return false
	}
	r, _ := utf8.DecodeRuneInString(string(s.syls[at]))
	return unicode.IsDigit(r) || unicode.Is(unicode.Latin, r)
}

func (s *Session) noteEnglishToken(k string) {
	if englishBoundary(k) {
		s.englishTokenBlocked = false
	} else if len([]rune(k)) == 1 {
		s.englishTokenBlocked = true
	}
}

// englishCompositionKey 處理接在中文組字裡的英文字（#372）。組字還有中文，所以沒有一個鍵交給程式（#106）：
// 字母接著打、Tab 接受反白的建議、英文候選開著時 ↑↓ 在清單裡移動、Esc 先關候選、退格先撤銷建議再刪字母，
// Enter 照畫面送出整段（#235）。其他鍵先把英文字照畫面變成組字裡的一般文字、收起英文候選，再照英文模式組字的規則處理：
// 空白、標點與數字插在游標處，←→ Home End 移動，↓ 開中文候選（#369），第二次 Esc 照「Esc 清除整段組字」的設定。
func (s *Session) englishCompositionKey(k string) (handled bool, commit string, active bool) {
	open := !s.private && !s.paused
	suggesting := open && s.cand != nil && s.cand.english
	switch {
	case k == proto.KeyTab:
		if open {
			s.acceptEnglish()
		}
		return true, "", true // 沒有建議時吃掉、英文字留著，同組字中的 Tab（#106）
	case k == proto.KeyEscape && (suggesting || s.mixed.accepted != ""):
		s.mixed.accepted = ""
		s.cand = nil
		s.englishDismissed = true
		return true, "", true
	case (k == proto.KeyUp || k == proto.KeyDown) && suggesting:
		delta := 1
		if k == proto.KeyUp {
			delta = -1
		}
		s.moveHighlight(delta)
		return true, "", true
	case k == proto.KeyBackspace:
		if s.mixed.accepted != "" {
			s.mixed.accepted = ""
		} else {
			s.mixed.raw = s.mixed.raw[:len(s.mixed.raw)-1]
			s.mixed.assistedPrefix = min(s.mixed.assistedPrefix, len(s.mixed.raw))
		}
		s.cand = nil
		s.englishDismissed = false
		if s.mixed.raw == "" {
			s.clearMixed()
		}
		return true, "", true
	case k == proto.KeyEnter:
		return true, s.commit(), true
	case open && englishLetter(k) && len(s.mixedText()) < english.MaxWordBytes:
		raw := s.mixedText()
		assistedPrefix := s.mixed.assistedPrefix
		if s.mixed.accepted != "" {
			assistedPrefix = len(raw)
		}
		s.mixed = pendingKeys{raw: raw + k, assistedPrefix: assistedPrefix}
		s.cand = nil
		s.englishDismissed = false
		return true, "", true
	}
	s.endEnglishInComposition()
	s.noteEnglishToken(k)
	return false, "", false
}

// endEnglishInComposition 把接在組字裡的英文字照畫面變成一般文字（含已接受的建議與學習來源），收起英文候選。
func (s *Session) endEnglishInComposition() {
	s.flushMixed()
	if s.cand != nil && s.cand.english {
		s.cand = nil
	}
}

func (s *Session) refreshEnglishCandidates() {
	if !s.englishWord || s.englishDismissed || s.mixed.accepted != "" || s.private || s.paused || len(s.mixed.raw) < 2 {
		if s.cand != nil && s.cand.english {
			s.cand = nil
		}
		return
	}
	if s.cand != nil {
		return
	}
	c := &candState{start: s.cursor, mixed: true, english: true}
	const limit = 6
	// Request one extra prefix because an exact match is not a completion.
	var prefixes, corrections []english.Candidate
	seen := map[string]bool{}
	// 「我的詞」排在字典前面，照詞條字面（#247）。只差大小寫也算建議（pcloud → pCloud），
	// 和打的完全相同才不是；ls 和 LS 是兩個詞，兩個都列。
	for _, word := range s.vocabEnglishWords(s.mixed.raw, limit+1) {
		if word != s.mixed.raw && len(prefixes) < limit {
			prefixes = append(prefixes, english.Candidate{Text: word})
			seen[strings.ToLower(word)] = true
		}
	}
	for _, word := range s.englishPrefix(s.mixed.raw, limit+1+len(prefixes)) {
		if len(prefixes) == limit {
			break
		}
		// 打完整個字時，常見寫法（iphone → iPhone，#358）也是建議；和打的完全相同才不是。
		whole := word.Text != s.mixed.raw && strings.EqualFold(word.Text, s.mixed.raw)
		if folded := strings.ToLower(word.Text); (len(word.Text) > len(s.mixed.raw) || whole) && !seen[folded] {
			prefixes = append(prefixes, word)
			seen[folded] = true
		}
	}
	// Corrections can overlap completions. Reserve up to three distinct
	// corrections while keeping prefix completion first for an unfinished word.
	// 親手加進「我的詞」的拼法和字典、學到的拼法一樣不修正。
	if !s.vocabEnglish(s.mixed.raw) {
		for _, word := range s.englishCorrections(s.mixed.raw, limit+3) {
			folded := strings.ToLower(word.Text)
			if !seen[folded] {
				seen[folded] = true
				corrections = append(corrections, word)
				if len(corrections) == 3 {
					break
				}
			}
		}
	}
	prefixCount := min(len(prefixes), limit-len(corrections))
	for _, word := range prefixes[:prefixCount] {
		c.items = append(c.items, rawCandidate(word.Text))
	}
	for _, word := range corrections {
		c.items = append(c.items, rawCandidate(word.Text))
	}
	if len(c.items) > 0 {
		s.cand = c
	}
}

func (s *Session) acceptEnglish() bool {
	if s.cand == nil || !s.cand.english {
		return false
	}
	return s.selectCandidate(s.candidateID(s.cand.cursor))
}
