package session

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
)

func requirePhoneticPreview(t *testing.T, snap proto.Snapshot, original, target string, start int) {
	t.Helper()
	if snap.Text() != original || snap.Candidates != nil || snap.SuggestionEdit == nil || snap.SuggestionEdit.Start != start || snap.SuggestionEdit.Text != target || snap.Suggestion != string([]rune(original)[start:])+" → "+target {
		t.Fatalf("unexpected preview: %+v", snap)
	}
}

func TestPhoneticTabPreviewAcceptAndLiteralEnter(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, accept := range []bool{false, true} {
			s := splitTypoSession()
			s.CandidateBeforeCursor = before
			snap := fzType(s, typoReading)
			requirePhoneticPreview(t, snap, "一年難盡", "一言難盡", 0)
			physical := slices.Clone(s.syls)
			want := "一年難盡"
			if accept {
				snap = key(s, proto.KeyTab)
				want = "一言難盡"
				if !snap.Handled || snap.Text() != want || snap.Commit != "" || snap.Candidates != nil || !slices.Equal(s.syls, physical) || s.locks[0].Matched != idiomReading || !s.locks[0].Completion {
					t.Fatalf("Tab did not accept exactly once: %+v, %+v", snap, s.locks)
				}
			}
			if got := key(s, proto.KeyEnter).Commit; got != want {
				t.Fatalf("commit %q, want %q", got, want)
			}
			if accept && (s.model.Store.Uni[want] != 0 || s.model.Store.Uses[want] != 0 || strings.Contains(strings.Join(s.Learned(), ","), want)) {
				t.Fatal("assisted repair learned as independent evidence")
			}
		}
	}
}

func TestPhoneticTabCompatibleManualChoiceAndUndo(t *testing.T) {
	for _, undoKey := range []string{proto.KeyBackspace, proto.KeyEscape} {
		s := splitTypoSession()
		fzType(s, typoReading)
		fzSelect(t, s, 3, 1, "盡")
		snap := s.Handle(proto.Request{Type: proto.Cursor, To: 4})
		requirePhoneticPreview(t, snap, "一年難盡", "一言難盡", 0)
		locks := maps.Clone(s.locks)
		key(s, proto.KeyTab)
		s.Handle(proto.Request{Type: proto.Display, Columns: 1})
		if got := key(s, undoKey).Text(); got != "一年難盡" || !maps.Equal(s.locks, locks) || len(s.syls) != 4 {
			t.Fatalf("undo lost original/locks: %q %+v", got, s.locks)
		}
		if got := key(s, proto.KeyEnter).Commit; got != "一年難盡" {
			t.Fatal(got)
		}
	}
}

func TestPhoneticTabProtectsManualTextAndUnrelatedPrefix(t *testing.T) {
	s := splitTypoSession()
	fzType(s, "ㄨㄛˇ "+typoReading)
	fzSelect(t, s, 0, 1, "我")
	snap := s.Handle(proto.Request{Type: proto.Cursor, To: 5})
	requirePhoneticPreview(t, snap, "我一年難盡", "一言難盡", 1)
	if got := s.Handle(proto.Request{Type: proto.Accept}).Text(); got != "我一言難盡" || s.locks[0].Word != "我" {
		t.Fatal(got, s.locks)
	}
	s = splitTypoSession()
	fzType(s, typoReading)
	fzSelect(t, s, 0, 2, "一年")
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	if s.phonetic != nil || key(s, proto.KeyTab).Text() != "一年難盡" {
		t.Fatal("Tab overwrote manually chosen 一年")
	}
	s = typoSession()
	fzType(s, "ㄊㄞˋ ㄆㄧㄥˊ ㄕㄥˋ ㄕˋ")
	fzSelect(t, s, 0, 4, "泰平盛世")
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	if key(s, proto.KeyTab).Text() != "泰平盛世" || key(s, proto.KeyEnter).Commit != "泰平盛世" {
		t.Fatal("deliberate name overwritten")
	}
}

func TestPhoneticTabContinuedTypingExpiresUndo(t *testing.T) {
	s := splitTypoSession()
	fzType(s, typoReading)
	key(s, proto.KeyTab)
	if got := typeSyl(s, "ㄨㄛˇ").Text(); got != "一言難盡我" {
		t.Fatal(got)
	}
	if s.phoneticUndo != nil {
		t.Fatal("further typing retained old undo")
	}
	key(s, proto.KeyBackspace)
	if len(s.syls) != 4 || s.locks[0].Matched != idiomReading {
		t.Fatal("ordinary Backspace undid the repair instead of new input")
	}
}

func TestPhoneticTabScopeAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		typed, target, reading string
		want                   bool
	}{
		{typoReading, "一言難盡", idiomReading, true},
		{typoReading + " ㄚ", "一言難盡啊", idiomReading + " ㄚ", true},
		{"ㄧ ㄋㄧㄢˊ ㄋㄢˊ ㄐㄧㄥˋ", "一言難盡", idiomReading, false},
		{"ㄧ ㄋㄧㄢˇ ㄋㄢˊ ㄐㄧㄣˋ", "一言難盡", idiomReading, false},
		{"ㄏㄢˋ ㄏㄨㄚˋ", "漫畫", "ㄇㄢˋ ㄏㄨㄚˋ", false},
		{"ㄌㄧㄣˊ ㄒㄧㄢ ㄕㄥ", "劉先生", "ㄌㄧㄡˊ ㄒㄧㄢ ㄕㄥ", false},
	} {
		s := fuzzySession(fuzzy.Config{}, append(fillers(tc.typed), word{tc.target, tc.reading, .1})...)
		fzType(s, tc.typed)
		if got := s.phonetic != nil && s.phonetic.entry.Surface == tc.target; got != tc.want {
			t.Fatalf("%s: preview %v, want %v", tc.typed, got, tc.want)
		}
	}
	s := splitTypoSession()
	fzType(s, typoReading)
	key(s, "r") // unfinished ㄐ hides the preview; Tab cannot accept stale text
	if s.phonetic != nil || key(s, proto.KeyTab).Text() == "一言難盡" {
		t.Fatal("accepted with pending phonetics")
	}
	key(s, proto.KeyBackspace)
	if snap := key(s, proto.KeyDown); snap.SuggestionEdit != nil {
		t.Fatal("preview visible over the picker")
	} else if _, ok := find(snap.Candidates.Items, "一言難盡"); ok {
		t.Fatal("whole-word row still inserted in short-word picker")
	}
	key(s, proto.KeyEscape)
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	if s.phonetic != nil {
		t.Fatal("preview visible while editing middle")
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	key(s, ",")
	if s.phonetic != nil {
		t.Fatal("repair crossed punctuation boundary")
	}
}

func TestPhoneticTabPrivateTransitionCannotAcceptStalePreview(t *testing.T) {
	s := splitTypoSession()
	fzType(s, typoReading)
	snap := s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyTab, Private: true})
	if snap.Text() != "一年難盡" || snap.SuggestionEdit != nil || s.phonetic != nil {
		t.Fatalf("private transition: %+v", snap)
	}
}

func TestPhoneticTabSurvivesPassiveTypoCommits(t *testing.T) {
	s := splitTypoSession()
	s.model.LM.(*lm.Mem).SetUni("一言難盡", .001)
	for range 4 {
		snap := fzType(s, typoReading)
		requirePhoneticPreview(t, snap, "一年難盡", "一言難盡", 0)
		key(s, proto.KeyEnter)
	}
}
