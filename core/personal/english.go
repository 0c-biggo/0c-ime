package personal

import (
	"math"
	"slices"
	"strings"

	"github.com/0c-biggo/0c-ime/core/english"
)

const MaxEnglishObservations = 1024

// Engineering defaults, replayable with the host's clock: ordinary words need
// three independent commits over at least two days; likely misspellings need
// eight over four days. Each word contributes at most two confirmations per day.
// Decayed thresholds leave room for observations on adjacent days.
type EnglishEvidence struct {
	Text                   string
	Confirmed, Days, Usage float64
	Updated, Day           int64
	DayConfirmed           int
	Suspicious             bool
}

type EnglishCommit struct {
	Text                           string
	Assisted, Explicit, Suspicious bool
}

// EnglishToken accepts names and identifiers, not URLs, paths, numeric-only
// strings, punctuation or non-ASCII case folding. Case is retained for display.
func EnglishToken(text string) bool {
	if len(text) < 3 || len(text) > english.MaxWordBytes {
		return false
	}
	for i := range text {
		c := text[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '_') {
			continue
		}
		return false
	}
	return true
}

func (e EnglishEvidence) at(now int64) EnglishEvidence {
	if now <= e.Updated {
		return e
	}
	e.Confirmed = decay(e.Confirmed, now-e.Updated, 90*daySeconds)
	e.Days = decay(e.Days, now-e.Updated, 180*daySeconds)
	e.Usage = decay(e.Usage, now-e.Updated, longHalfLife)
	e.Updated = max(e.Updated, now)
	if now/daySeconds > e.Day {
		e.Day, e.DayConfirmed = now/daySeconds, 0
	}
	return e
}

func (e EnglishEvidence) eligible() bool {
	if e.Suspicious {
		return e.Confirmed >= 7.5 && e.Days >= 3.5
	}
	return e.Confirmed >= 2.5 && e.Days >= 1.5
}

// LearnEnglish is called exactly once at commit, using final tokens and their
// origins. Repeated words in one composition count once. System suggestions may
// strengthen usage of an existing observation but never confirm or create one.
func (m *Model) LearnEnglish(events []EnglishCommit) {
	s := m.Store
	if s.English == nil {
		s.English = map[string]EnglishEvidence{}
	}
	seen := map[string]bool{}
	for _, event := range events {
		if !EnglishToken(event.Text) {
			continue
		}
		key := strings.ToLower(event.Text)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 64 {
			break
		}
		if s.Tombstones[key] {
			if !event.Explicit || event.Assisted {
				continue
			}
			delete(s.Tombstones, key)
		}
		e, exists := s.English[key]
		if event.Assisted && !exists {
			continue
		}
		if !exists {
			e = EnglishEvidence{Text: event.Text, Updated: s.Time, Day: s.Time / daySeconds}
		}
		e = e.at(s.Time)
		weight := 0.25
		if !event.Assisted {
			weight = 1
			e.Text = event.Text
			e.Suspicious = e.Suspicious || event.Suspicious
			if e.DayConfirmed < 2 {
				if e.DayConfirmed == 0 {
					e.Days = min(16, e.Days+1)
				}
				e.Confirmed = min(32, e.Confirmed+1)
				e.DayConfirmed++
			}
		}
		e.Usage = min(16, e.Usage+weight)
		s.English[key] = e
		m.Gen++
	}
	s.boundEnglish()
}

func (s *Store) boundEnglish() {
	if len(s.English) <= MaxEnglishObservations {
		return
	}
	type entry struct {
		key      string
		strength float64
	}
	entries := make([]entry, 0, len(s.English))
	for key, e := range s.English {
		e = e.at(s.Time)
		entries = append(entries, entry{key, e.Confirmed + e.Days + 0.1*e.Usage})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if a.strength < b.strength {
			return -1
		}
		if a.strength > b.strength {
			return 1
		}
		return strings.Compare(a.key, b.key)
	})
	for _, e := range entries[:len(entries)-MaxEnglishObservations] {
		delete(s.English, e.key)
	}
}

func (s *Store) cleanEnglish() {
	if s.English == nil {
		s.English = map[string]EnglishEvidence{}
	}
	for key, e := range s.English {
		valid := EnglishToken(e.Text) && strings.ToLower(e.Text) == key && e.DayConfirmed >= 0 && e.DayConfirmed <= 2
		for _, v := range []float64{e.Confirmed, e.Days, e.Usage} {
			valid = valid && v >= 0 && v <= 32 && !math.IsNaN(v) && !math.IsInf(v, 0)
		}
		valid = valid && e.Days <= 16 && e.Usage <= 16
		if !valid || s.Tombstones[key] {
			delete(s.English, key)
		}
	}
	s.boundEnglish()
}

func (m *Model) forgetEnglish(text string) {
	if !EnglishToken(text) {
		return
	}
	key := strings.ToLower(text)
	delete(m.Store.English, key)
	m.Store.Tombstones[key] = true
}

func (m *Model) EnglishKnown(text string) bool {
	if !EnglishToken(text) {
		return false
	}
	key := strings.ToLower(text)
	e, ok := m.Store.English[key]
	return ok && !m.Store.Tombstones[key] && e.at(m.Store.Time).eligible()
}

func englishRank(cs []english.Candidate, limit int) []english.Candidate {
	slices.SortFunc(cs, func(a, b english.Candidate) int {
		if a.Distance < b.Distance {
			return -1
		}
		if a.Distance > b.Distance {
			return 1
		}
		if a.Weight > b.Weight {
			return -1
		}
		if a.Weight < b.Weight {
			return 1
		}
		return strings.Compare(a.Text, b.Text)
	})
	out := cs[:0]
	seen := map[string]bool{}
	for _, c := range cs {
		key := strings.ToLower(c.Text)
		if !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	return out[:min(len(out), limit)]
}

func (m *Model) EnglishPrefix(base *english.Dictionary, prefix string, limit int) []english.Candidate {
	if limit <= 0 || prefix == "" || len(prefix) > english.MaxWordBytes {
		return nil
	}
	// Prefixes can be short; validate with a neutral suffix without case folding.
	if !EnglishToken(prefix+"aaa") && !EnglishToken(prefix) {
		return base.Prefix(prefix, limit)
	}
	limit = min(limit, english.MaxCandidates)
	out := base.Prefix(prefix, limit)
	p := strings.ToLower(prefix)
	for key, e := range m.Store.English {
		e = e.at(m.Store.Time)
		if !e.eligible() || m.Store.Tombstones[key] || !strings.HasPrefix(key, p) || base.Has(key) && !base.Suggestible(key) {
			continue
		}
		suffix := e.Text[len(prefix):]
		if prefix == strings.ToUpper(prefix) {
			suffix = strings.ToUpper(suffix)
		}
		out = append(out, english.Candidate{Text: prefix + suffix, Weight: 320 + e.Usage})
	}
	return englishRank(out, limit)
}

func (m *Model) EnglishCorrections(base *english.Dictionary, text string, limit int) []english.Candidate {
	if limit <= 0 || m.EnglishKnown(text) {
		return nil
	}
	limit = min(limit, english.MaxCandidates)
	out := base.Corrections(text, limit)
	if base.Has(text) {
		return out
	}
	if m.englishIndex == nil || m.englishIndexStore != m.Store || m.englishIndexBase != base || m.englishIndexGen != m.Gen {
		words := []english.Word{}
		for key, e := range m.Store.English {
			e = e.at(m.Store.Time)
			if e.eligible() && !m.Store.Tombstones[key] && !(base.Has(key) && !base.Suggestible(key)) {
				words = append(words, english.Word{Text: key, Weight: 320 + e.Usage})
			}
		}
		m.englishIndex = english.New(words)
		m.englishIndexStore, m.englishIndexBase, m.englishIndexGen = m.Store, base, m.Gen
	}
	for _, c := range m.englishIndex.Corrections(text, limit) {
		stored := m.Store.English[strings.ToLower(c.Text)].Text
		if text == strings.ToUpper(text) {
			stored = strings.ToUpper(stored)
		} else if text[0] >= 'A' && text[0] <= 'Z' {
			stored = strings.ToUpper(stored[:1]) + stored[1:]
		}
		c.Text = stored
		out = append(out, c)
	}
	return englishRank(out, limit)
}
