package session

import (
	"strings"
	"unicode"

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

// Only a word started in an empty English composition belongs to this feature.
// Existing Chinese composition kept by a mode switch retains its old editing rules.
func (s *Session) englishKey(k string) (handled bool, commit string, active bool) {
	if !s.englishWord {
		if s.composing() {
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
	commit = s.commit()
	s.englishTokenBlocked = !englishBoundary(k)
	return false, commit, true
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
	for _, word := range s.englishPrefix(s.mixed.raw, limit+1) {
		if len(word.Text) > len(s.mixed.raw) {
			prefixes = append(prefixes, word)
			seen[strings.ToLower(word.Text)] = true
			if len(prefixes) == limit {
				break
			}
		}
	}
	// Corrections can overlap completions. Reserve up to three distinct
	// corrections while keeping prefix completion first for an unfinished word.
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
