package session

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/decode"
	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

type phoneticHint struct {
	start   int
	replace string
	entry   lexicon.Entry
}

type phoneticHintCache struct {
	key  string
	hint *phoneticHint
}

type phoneticUndo struct {
	locks map[int]decode.Lock
	path  []decode.Seg
	auto  []AutoChange
}

// Preview only completed words at the composition tail. The physical reading,
// displayed text and learning remain unchanged until explicit Tab/Accept.
// Cache negative results too: refresh runs multiple times per physical key.
func (s *Session) refreshPhoneticHint() {
	s.phonetic = nil
	if s.english || s.private || s.paused || s.mixed.raw != "" || s.cursor != len(s.syls) || !s.comp.Empty() || s.cand != nil || len(s.syls) < 4 || len(s.path) == 0 {
		return
	}
	key := fmt.Sprintf("%d|%s|%v", s.model.Gen, s.pathSig, s.path)
	if key != s.phoneticCache.key {
		s.phoneticCache = phoneticHintCache{key: key, hint: s.findPhoneticHint()}
	}
	s.phonetic = s.phoneticCache.hint
}

func (s *Session) findPhoneticHint() *phoneticHint {
	var best *phoneticHint
	bestGain := 0.0
	lat := input.Exact{}.Expand(s.syls)
	for i, seg := range s.path {
		start, end := seg.Start, len(s.syls)
		length := end - start
		if length < 4 || length > decode.MaxWordLen {
			continue
		}
		w, ok := s.phoneticWindow(lat, start, length)
		if !ok {
			continue // literals and incomplete/invalid readings are boundaries
		}
		var text strings.Builder
		left, probability := lm.BOS, s.model.SystemProb
		if weight := s.Opt.DocumentContextWeight; weight > 0 && weight <= 1 {
			if context := s.contextLeft(start); context != lm.BOS {
				scorer := &documentScorer{Model: s.model, left: context, weight: weight}
				probability = scorer.SystemProb
			}
		}
		if i > 0 && !s.literals[s.path[i-1].Start] {
			left = s.path[i-1].Word
		}
		original, previous := 0.0, left
		valid := true
		for _, g := range s.path[i:] {
			if s.literals[g.Start] || utf8.RuneCountInString(g.Word) != g.End-g.Start || g.Matched != "" {
				valid = false
				break
			}
			text.WriteString(g.Word)
			original += math.Log(probability(previous, g.Word))
			previous = g.Word
		}
		if !valid {
			continue
		}
		replace := text.String()
		// A known complete system spelling needs no proactive repair.
		// Automatically learned phrases are not proof of intentional spelling;
		// explicit manual choices are protected by phoneticLocksAllow.
		if slices.ContainsFunc(s.model.Lex.Exact(s.syls[start:end]), func(e lexicon.Entry) bool {
			return !e.Correction && e.Surface == replace
		}) {
			continue
		}
		var walk func(int, []zhuyin.Syllable, float64)
		walk = func(at int, reading []zhuyin.Syllable, cost float64) {
			if at == end {
				if cost == 0 {
					return
				}
				for _, e := range s.dec.Lex.Exact(reading) {
					if !w.Accept(e) || e.Rare || e.Surface == replace || !s.phoneticLocksAllow(start, e) {
						continue
					}
					// Compare against the currently displayed segmentation using
					// system statistical model and the picker's one-edit cost.
					// Passive typo commits must not immediately suppress the hint.
					// This is a preview ranking, not a confidence score.
					gain := math.Log(probability(left, e.Surface)) - cost - original
					if gain > bestGain {
						bestGain, best = gain, &phoneticHint{start, replace, e}
					}
				}
				return
			}
			for _, h := range w.Lat[at] {
				if cost+h.Cost > w.MaxCost {
					continue
				}
				next := append(reading[:len(reading):len(reading)], h.Syl)
				if s.dec.Lex.HasPrefix(next) {
					walk(at+1, next, cost+h.Cost)
				}
			}
		}
		walk(start, nil, 0)
	}
	return best
}

// Compatible manual choices (e.g. selecting the final 盡) may be contained in
// the word, but neither their text nor their chosen reading may change.
func (s *Session) phoneticLocksAllow(start int, e lexicon.Entry) bool {
	word, end := []rune(e.Surface), start+len(e.Reading)
	for at, lock := range s.locks {
		if at >= end || at+lock.Len <= start {
			continue
		}
		if at < start || at+lock.Len > end || string(word[at-start:at-start+lock.Len]) != lock.Word {
			return false
		}
		reading := s.syls[at : at+lock.Len]
		if lock.Matched != "" {
			reading = zhuyin.ParseReading(lock.Matched)
		}
		if !slices.Equal(reading, e.Reading[at-start:at-start+lock.Len]) {
			return false
		}
	}
	return true
}

func (s *Session) acceptPhonetic() bool {
	h := s.phonetic
	if h == nil || s.english || s.private || s.paused || s.cursor != len(s.syls) || !s.comp.Empty() || s.cand != nil || !s.phoneticLocksAllow(h.start, h.entry) {
		return false
	}
	u := &phoneticUndo{maps.Clone(s.locks), slices.Clone(s.path), slices.Clone(s.autoLog)}
	for at := range s.locks {
		if at >= h.start {
			delete(s.locks, at)
		}
	}
	matched := zhuyin.Key(h.entry.Reading)
	s.locks[h.start] = decode.Lock{Word: h.entry.Surface, Len: len(h.entry.Reading), Completion: true, Matched: matched}
	s.previewRewrite = false
	s.refreshSelection(u.path, u.auto, decode.Seg{Word: h.entry.Surface, Start: h.start, End: len(s.syls), Matched: matched})
	s.phoneticUndo = u
	return true
}

// The immediately following Backspace/Escape undoes acceptance, including the
// old manual locks and exactly the text the user saw. Physical syllables were
// never rewritten. Any further editing/navigation expires this undo record.
func (s *Session) undoPhonetic() bool {
	u := s.phoneticUndo
	if u == nil {
		return false
	}
	s.phoneticUndo = nil
	s.locks = u.locks
	s.pathSig = ""
	s.refresh()
	s.path, s.autoLog = u.path, u.auto
	s.shownPath, s.shownSyls = slices.Clone(s.path), slices.Clone(s.syls)
	s.hint = nil
	s.displayed = map[int]string{}
	for _, g := range s.path {
		for i, r := range []rune(g.Word) {
			if !s.literals[g.Start+i] {
				s.displayed[g.Start+i] = string(r)
			}
		}
	}
	return true
}
