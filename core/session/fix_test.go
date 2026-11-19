package session

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// pickFromCandidates is the way to the same choice through the candidate window:
// finish the syllable, open the length at start, select the word.
func pickFromCandidates(t *testing.T, s *Session, start, length int, word string) proto.Snapshot {
	t.Helper()
	s.CandidateBeforeCursor = false
	s.Handle(proto.Request{Type: proto.Candidates})
	s.Handle(proto.Request{Type: proto.Close})
	s.Handle(proto.Request{Type: proto.Cursor, To: start})
	v := s.Handle(proto.Request{Type: proto.Candidates, Length: length})
	if v.Candidates == nil {
		t.Fatalf("no candidates of length %d at %d", length, start)
	}
	for _, c := range v.Candidates.Items {
		if c.Text == word {
			return s.Handle(proto.Request{Type: proto.Select, Index: c.ID})
		}
	}
	t.Fatalf("%q not offered: %+v", word, v.Candidates.Items)
	return proto.Snapshot{}
}

func typeSymbols(s *Session, keys string) {
	for _, k := range keys {
		s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(k)})
	}
}

// fix gives the same result as choosing the word from the window, toned or not:
// the same composition, the same lock, the same replaced words to learn from, and
// the same text sent.
func TestFixMatchesSelect(t *testing.T) {
	for _, c := range []struct {
		name          string
		toneless      bool
		keys          string
		start, length int
		word          string
	}{
		{"word", false, "ㄈㄥˉㄐㄧㄥˇㄨㄛˇ", 0, 2, "風景"},
		{"single", false, "ㄈㄥˉㄐㄧㄥˇㄨㄛˇ", 1, 1, "井"},
		{"toneless word", true, "ㄈㄥㄐㄧㄥㄨㄛ", 0, 2, "風景"},
		{"toneless single, last syllable open", true, "ㄈㄥㄐㄧㄥ", 1, 1, "井"},
		{"first symbols only", true, "ㄈㄐㄨ", 0, 2, "風景"},
		{"first symbols only, single", true, "ㄈㄐㄨ", 1, 1, "井"},
	} {
		t.Run(c.name, func(t *testing.T) {
			open := func() *Session {
				s := newSession()
				s.Toneless, s.Abbreviation, s.MixedWithToneless = c.toneless, c.toneless, c.toneless
				typeSymbols(s, c.keys)
				return s
			}
			a, b := open(), open()
			want := pickFromCandidates(t, a, c.start, c.length, c.word)
			got := b.Handle(proto.Request{Type: proto.Fix, To: c.start, Length: c.length, Word: c.word})
			if slices.Contains(got.Notices, "沒有這個候選") || got.Text() != want.Text() {
				t.Fatalf("fix %q: %q %v, select: %q", c.word, got.Text(), got.Notices, want.Text())
			}
			if !reflect.DeepEqual(a.locks, b.locks) || !reflect.DeepEqual(a.rejected, b.rejected) || !reflect.DeepEqual(a.path, b.path) {
				t.Fatalf("state differs:\nselect %v %v %v\nfix    %v %v %v", a.locks, a.rejected, a.path, b.locks, b.rejected, b.path)
			}
			if x, y := a.Handle(proto.Request{Type: proto.Commit}).Commit, b.Handle(proto.Request{Type: proto.Commit}).Commit; x != y || x == "" {
				t.Fatalf("sent %q, want %q", y, x)
			}
		})
	}
}

// A word that does not fit leaves everything as it was.
func TestFixRefusesWhatDoesNotFit(t *testing.T) {
	for _, c := range []struct {
		name          string
		start, length int
		word          string
	}{
		{"wrong reading", 0, 2, "麻煩"},
		{"length differs from the word", 0, 1, "風景"},
		{"past the end", 2, 2, "我想"},
		{"negative start", -1, 1, "風"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSession()
			typeSymbols(s, "ㄈㄥˉㄐㄧㄥˇㄨㄛˇ")
			before := s.View()
			locks, path := maps.Clone(s.locks), slices.Clone(s.path)
			got := s.Handle(proto.Request{Type: proto.Fix, To: c.start, Length: c.length, Word: c.word})
			if !slices.Contains(got.Notices, "沒有這個候選") {
				t.Fatalf("accepted: %+v", got)
			}
			if got.Text() != before.Text() || !reflect.DeepEqual(s.locks, locks) || !reflect.DeepEqual(s.path, path) || len(s.rejected) != 0 {
				t.Fatalf("changed: %q, locks %v, path %v, rejected %v", got.Text(), s.locks, s.path, s.rejected)
			}
		})
	}
}

// fix 失敗時什麼都不改，未打完的注音也一樣：不能先不打聲調把它收成音節（finishUntoned）才發現放不進去。
func TestFailedFixKeepsPendingZhuyin(t *testing.T) {
	for _, c := range []struct {
		name, word    string
		start, length int
	}{
		{"does not fit", "井", 0, 1},
		{"out of range", "我吃", 1, 2},
		{"length mismatch", "我吃", 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := coexistSession()
			symbolType(s, "ㄨㄛˇㄔ")
			got := s.Handle(proto.Request{Type: proto.Fix, To: c.start, Length: c.length, Word: c.word})
			if !slices.Contains(got.Notices, "沒有這個候選") {
				t.Fatalf("accepted: %+v", got)
			}
			if s.comp.String() != "ㄔ" || len(s.syls) != 1 || len(s.locks) != 0 {
				t.Fatalf("changed: comp %q syls %v locks %v", s.comp.String(), s.syls, s.locks)
			}
			if snap := s.Handle(proto.Request{Type: proto.Commit}); snap.Commit != "我吃" {
				t.Fatalf("after the failed fix: %q", snap.Commit)
			}
		})
	}
}
