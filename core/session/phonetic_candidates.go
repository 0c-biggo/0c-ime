package session

import (
	"slices"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/decode"
	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// oneSymbolApart compares phonetic symbols only. Tone and syllable boundaries
// are fixed; an identical reading is already covered by ordinary candidates.
func oneSymbolApart(a, b zhuyin.Syllable) bool {
	if a.ToneOf() != b.ToneOf() || a == b {
		return false
	}
	x, y := []rune(a.Base()), []rune(b.Base())
	if len(x) > len(y) {
		x, y = y, x
	}
	if len(y)-len(x) > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < len(x) && j < len(y) {
		if x[i] == y[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		if len(x) == len(y) {
			i++
		}
		j++
	}
	return edits+len(y)-j == 1
}

// phoneticWindow supplies explicit candidates and Tab previews only. It never
// changes decoded text or accepts input on its own. Each
// suggestion is one complete 4–8 character lexicon entry with one symbol edit
// in total, independent of dialect fuzzy settings. Context and accepted locks
// outside the window retain their existing readings.
func (s *Session) phoneticWindow(lat input.Lattice, pos, length int) (decode.Window, bool) {
	if length < 4 || length > decode.MaxWordLen {
		return decode.Window{}, false
	}
	wide := slices.Clone(lat)
	for at := pos; at < pos+length; at++ {
		typed := s.syls[at]
		if !typed.Legal() {
			return decode.Window{}, false
		}
		// Start from physical input, not an expanded frontend or prior repair.
		wide[at] = []input.Hyp{{Syl: typed}}
		for _, base := range phoneticNeighbours[typed.Base()] {
			wide[at] = append(wide[at], input.Hyp{Syl: base.WithTone(typed.ToneOf()), Cost: 1})
		}
	}
	return decode.Window{
		Lat: wide, MaxCost: 1, MaxCandidates: 32, OnlyCosted: true,
		Accept: func(e lexicon.Entry) bool {
			return !e.Correction && len(e.Reading) == length && utf8.RuneCountInString(e.Surface) == length
		},
	}, true
}

// Immutable base-reading neighbours avoid rescanning the inventory on every
// completed syllable. Tones are applied when a query window is constructed.
var phoneticNeighbours = func() map[zhuyin.Syllable][]zhuyin.Syllable {
	out := map[zhuyin.Syllable][]zhuyin.Syllable{}
	for _, a := range zhuyin.Inventory() {
		for _, b := range zhuyin.Inventory() {
			if oneSymbolApart(a, b) {
				out[a] = append(out[a], b)
			}
		}
	}
	return out
}()
