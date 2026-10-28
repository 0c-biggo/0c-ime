package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
)

func autocompleteSession() *Session {
	s := newSession()
	s.EnglishAutocomplete = true
	s.English = english.New([]english.Word{{Text: "hello", Weight: 100}, {Text: "help", Weight: 50}, {Text: "world", Weight: 10}})
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	return s
}

func TestEnglishAutocompleteExplicitSelectionAndUndo(t *testing.T) {
	s := autocompleteSession()
	snap := mixedType(s, "hel")
	if snap.Text() != "hel" || snap.Candidates == nil || snap.Candidates.Items[0].Text != "hello" || !snap.EnglishPending {
		t.Fatal(snap)
	}
	key(s, proto.KeyDown)
	if snap := key(s, proto.KeyTab); snap.Text() != "help" || snap.Candidates != nil {
		t.Fatal(snap)
	}
	if snap := key(s, proto.KeyBackspace); snap.Text() != "hel" {
		t.Fatal(snap)
	}
	key(s, proto.KeyTab)
	if snap := key(s, proto.KeyEscape); snap.Text() != "hel" || snap.Candidates != nil {
		t.Fatal(snap)
	}
	if snap := key(s, proto.KeyTab); snap.Handled || snap.Commit != "hel" || snap.Text() != "" {
		t.Fatal(snap)
	}
	mixedType(s, "hel")
	pickText(t, s, "hello")
	if snap := key(s, " "); snap.Handled || snap.Commit != "hello" {
		t.Fatal(snap)
	}
}

// #235（GitHub 0c-ime#35）：英文模式打 `cd Doc` 按 Enter，只送出畫面上有底線的組字，不連 Enter 一起交給程式；
// Windows Terminal 會直接執行命令，網頁的文字框會多一行。同注音組字與中英混打；再按一次 Enter 才交給程式。
// 有補字候選時 Enter 也不替使用者接受建議。
func TestEnglishAutocompleteEnterOnlyCommits(t *testing.T) {
	for _, typed := range []string{"cd Doc", "hel", "he", "zz", "Hel"} {
		s := autocompleteSession()
		var app strings.Builder
		for _, r := range typed {
			snap := key(s, string(r))
			app.WriteString(snap.Commit)
			if !snap.Handled {
				app.WriteRune(r)
			}
		}
		snap := key(s, proto.KeyEnter)
		app.WriteString(snap.Commit)
		if !snap.Handled || app.String() != typed || snap.Text() != "" || snap.Candidates != nil || snap.EnglishPending {
			t.Fatalf("%q: Enter must commit only: app=%q %+v", typed, app.String(), snap)
		}
		if again := key(s, proto.KeyEnter); again.Handled || again.Commit != "" {
			t.Fatalf("%q: idle Enter must reach the app: %+v", typed, again)
		}
		if next := mixedType(s, "hel"); !next.EnglishPending || next.Candidates == nil {
			t.Fatalf("%q: a new word must start after Enter: %+v", typed, next)
		}
	}
}

func TestEnglishAutocompleteBoundariesKeepAppActions(t *testing.T) {
	for _, k := range []string{" ", proto.KeyLeft, proto.KeyRight, proto.KeyHome, proto.KeyEnd, proto.KeyDelete, proto.KeyPageDown, "BackTab", ".", "1"} {
		s := autocompleteSession()
		mixedType(s, "hel")
		snap := key(s, k)
		if snap.Handled || snap.Commit != "hel" || snap.Text() != "" || snap.Candidates != nil {
			t.Fatalf("%s: %+v", k, snap)
		}
		if len(s.model.Store.Uni) != 0 || len(s.model.Store.English) != 0 {
			t.Fatal("English polluted Chinese learning")
		}
	}
	s := autocompleteSession()
	mixedType(s, "zz")
	if snap := key(s, proto.KeyTab); snap.Handled || snap.Commit != "zz" {
		t.Fatal(snap)
	}
}

func TestEnglishAutocompleteURLsIdentifiersAndBoundedInput(t *testing.T) {
	for _, raw := range []string{"https://hello.world/test", "hel_name123", "name@example.com", "123hello", strings.Repeat("a", 120)} {
		s := autocompleteSession()
		var app strings.Builder
		for _, k := range raw + " " {
			snap := key(s, string(k))
			app.WriteString(snap.Commit)
			if !snap.Handled {
				app.WriteRune(k)
			}
			if s.englishTokenBlocked && snap.Candidates != nil {
				t.Fatal(snap)
			}
		}
		if app.String() != raw+" " {
			t.Fatalf("%q -> %q", raw, app.String())
		}
		if snap := mixedType(s, "hel"); snap.Candidates == nil {
			t.Fatal("whitespace did not clear token suppression")
		}
	}
}

func TestEnglishAutocompleteProtectionAndLegacyComposition(t *testing.T) {
	for _, private := range []bool{true, false} {
		s := autocompleteSession()
		if !private {
			s.Handle(proto.Request{Type: proto.Pause, On: true})
		}
		if snap := s.Handle(proto.Request{Type: proto.Key, Key: "h", Private: private}); snap.Handled || snap.EnglishPending || snap.Candidates != nil {
			t.Fatal(snap)
		}
	}
	s := autocompleteSession()
	mixedType(s, "hel")
	if snap := s.Handle(proto.Request{Type: proto.Key, Key: "p", Private: true}); snap.Handled || snap.Commit != "hel" || snap.Candidates != nil {
		t.Fatal(snap)
	}
	s = autocompleteSession()
	for range 3 {
		mixedType(s, "h")
		s.Handle(proto.Request{Type: proto.Backspace})
	}
	if snap := key(s, proto.KeyBackspace); snap.Handled {
		t.Fatal(snap)
	}
	s = autocompleteSession()
	mixedType(s, "hel")
	if snap := s.Handle(proto.Request{Type: proto.Cursor, To: 0}); snap.EnglishPending {
		t.Fatal(snap)
	}
	s = newSession()
	s.EnglishAutocomplete = true
	mixedType(s, "ji3")
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := mixedType(s, "hel"); snap.Text() != "我hel" || snap.Candidates != nil || snap.EnglishPending {
		t.Fatal(snap)
	}
	s = newSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, "h"); snap.Handled || snap.Text() != "" {
		t.Fatal("default changed", snap)
	}
}

func TestEnglishAutocompletePreservesCase(t *testing.T) {
	for _, raw := range []string{"Hel", "HEL"} {
		s := autocompleteSession()
		mixedType(s, raw)
		snap := key(s, proto.KeyTab)
		want := "Hello"
		if raw == "HEL" {
			want = "HELLO"
		}
		if snap.Text() != want {
			t.Fatalf("%s -> %s", raw, snap.Text())
		}
		if end := key(s, proto.KeyEnter); !end.Handled || end.Commit != want {
			t.Fatal(end)
		}
	}
}

func TestEnglishAutocompleteCandidateRequestsStayEnglish(t *testing.T) {
	s := autocompleteSession()
	mixedType(s, "hel")
	s.Handle(proto.Request{Type: proto.Close})
	snap := s.Handle(proto.Request{Type: proto.Candidates})
	if snap.Candidates == nil || snap.Candidates.Items[0].Text != "hello" || !s.cand.english {
		t.Fatal(snap)
	}
	snap = s.Handle(proto.Request{Type: proto.Forget, Word: "hello"})
	if snap.Candidates != nil && !s.cand.english {
		t.Fatal("Forget opened mixed interpretation", snap)
	}
	s.Handle(proto.Request{Type: proto.Close})
	snap = s.Handle(proto.Request{Type: proto.Backspace})
	if snap.Text() != "he" || snap.Candidates == nil {
		t.Fatal(snap)
	}
}

func TestEnglishAutocompleteSwitchIsIndependentOfMixedInput(t *testing.T) {
	s := mixedSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, "h"); snap.Handled {
		t.Fatal("mixed input enabled English capture without its own switch", snap)
	}
	s = autocompleteSession()
	s.MixedInput = true
	s.model.AdvanceTime(100 * englishDay)
	mixedType(s, "hel")
	key(s, proto.KeyTab)
	key(s, proto.KeyEnter)
	if len(s.model.Store.English) != 0 {
		t.Fatal("accepting a system completion independently confirmed a word", s.model.Store.English)
	}
}

func TestEnglishAutocompletePauseFinishesWordBeforePassthrough(t *testing.T) {
	s := autocompleteSession()
	mixedType(s, "hel")
	paused := s.Handle(proto.Request{Type: proto.Pause, On: true})
	if paused.Commit != "hel" || paused.EnglishPending || paused.EnglishCanStart || paused.Candidates != nil {
		t.Fatal(paused)
	}
	if snap := key(s, "p"); snap.Handled || snap.Commit != "" {
		t.Fatal(snap)
	}
	if len(s.model.Store.English) != 0 {
		t.Fatal("pause learned an unfinished word")
	}
}

func TestEnglishAutocompleteEditedCompletionDoesNotConfirm(t *testing.T) {
	for _, existing := range []bool{false, true} {
		s := autocompleteSession()
		s.MixedInput = true
		s.model.AdvanceTime(100 * englishDay)
		if existing {
			s.model.LearnEnglish([]personal.EnglishCommit{{Text: "hellos"}})
		}
		before := s.model.Store.English["hellos"]
		mixedType(s, "hel")
		key(s, proto.KeyTab)
		mixedType(s, "sx")
		key(s, proto.KeyBackspace)
		key(s, proto.KeyEscape)
		end := key(s, proto.KeyEnter)
		if !end.Handled || end.Commit != "hellos" {
			t.Fatalf("edited completion must retain displayed word: %+v", end)
		}
		after := s.model.Store.English["hellos"]
		if after.Confirmed != before.Confirmed || after.DayConfirmed != before.DayConfirmed || !existing && len(s.model.Store.English) != 0 {
			t.Fatalf("edited suggestion was independently confirmed: before=%+v after=%+v", before, after)
		}
		// 清空後自行輸入的新單字仍可確認。
		mixedType(s, "brandnew")
		key(s, proto.KeyEnter)
		if e := s.model.Store.English["brandnew"]; e.Confirmed != 1 {
			t.Fatalf("new independently typed word did not confirm: %+v", e)
		}
	}
}

func TestEnglishAutocompleteTokenPrefixesLeaveNoLearning(t *testing.T) {
	for _, raw := range []string{"acmex@example.com", "myname_abc", "myname123", "https://example.com", "filepath.txt", "folder/file", "name-value", strings.Repeat("a", 80)} {
		t.Run(raw, func(t *testing.T) {
			s := autocompleteSession()
			s.MixedInput = true
			s.model.AdvanceTime(100 * englishDay)
			var app strings.Builder
			for _, r := range raw + " " {
				snap := key(s, string(r))
				app.WriteString(snap.Commit)
				if !snap.Handled {
					app.WriteRune(r)
				}
			}
			if app.String() != raw+" " || len(s.model.Store.English) != 0 {
				t.Fatalf("token was changed or its prefix learned: %q %+v", app.String(), s.model.Store.English)
			}
			mixedType(s, "brandnew")
			key(s, proto.KeyEnter)
			if e := s.model.Store.English["brandnew"]; e.Confirmed != 1 {
				t.Fatalf("space must allow next standalone word to confirm: %+v", e)
			}
		})
	}
}

func TestEnglishAutocompleteAssistedCharactersSurviveMaterialization(t *testing.T) {
	s := autocompleteSession()
	s.MixedInput = true
	s.model.AdvanceTime(100 * englishDay)
	mixedType(s, "hel")
	key(s, proto.KeyTab)
	mixedType(s, "sx")
	s.Handle(proto.Request{Type: proto.Backspace})
	s.Handle(proto.Request{Type: proto.Cursor, To: 6})
	for pos := 0; pos < 6; pos++ {
		if origin, ok := s.mixedOrigins[pos]; !ok || origin.assisted != (pos < 5) {
			t.Fatalf("completion and typed suffix must retain distinct origins at %d: %+v", pos, origin)
		}
	}
	if end := key(s, proto.KeyEnter); end.Commit != "hellos" || len(s.model.Store.English) != 0 {
		t.Fatalf("materialized completion confirmed: %+v %+v", end, s.model.Store.English)
	}
	// 撤銷接受會還原實際按鍵，之後自行輸入的字仍能正常累積確認。
	mixedType(s, "hel")
	key(s, proto.KeyTab)
	key(s, proto.KeyBackspace)
	key(s, "m")
	key(s, proto.KeyEnter)
	if e := s.model.Store.English["helm"]; e.Confirmed != 1 {
		t.Fatalf("undo should restore independently typed origins: %+v", e)
	}
}

func TestEnglishAutocompleteStandaloneWordStillConfirms(t *testing.T) {
	for _, boundary := range []string{" ", proto.KeyEnter, ",", "!", ";"} {
		s := autocompleteSession()
		s.MixedInput = true
		s.model.AdvanceTime(100 * englishDay)
		mixedType(s, "brandnew")
		snap := key(s, boundary)
		if snap.Handled != (boundary == proto.KeyEnter) || snap.Commit != "brandnew" || s.model.Store.English["brandnew"].Confirmed != 1 {
			t.Fatalf("standalone word must confirm at %q: %+v %+v", boundary, snap, s.model.Store.English)
		}
	}
}

func TestEnglishAutocompleteSpellingCorrectionRequiresAcceptance(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"recieve", "receive"}, {"helo", "hello"}, {"helllo", "hello"},
		{"teh", "the"}, {"Recieve", "Receive"}, {"RECIEVE", "RECEIVE"},
	} {
		t.Run(tc.typed, func(t *testing.T) {
			s := autocompleteSession()
			s.English = english.Prototype()
			snap := mixedType(s, tc.typed)
			if snap.Text() != tc.typed || snap.Candidates == nil || snap.Candidates.Items[0].Text != tc.want {
				t.Fatalf("correction must leave typed spelling visible: %+v", snap)
			}
			if snap := key(s, proto.KeyTab); !snap.Handled || snap.Text() != tc.want || snap.Candidates != nil {
				t.Fatalf("Tab did not accept correction: %+v", snap)
			}
			if snap := key(s, proto.KeyEnter); !snap.Handled || snap.Commit != tc.want {
				t.Fatalf("accepted correction was not committed: %+v", snap)
			}
			for _, boundary := range []string{" ", proto.KeyEnter} {
				mixedType(s, tc.typed)
				if snap := key(s, boundary); snap.Handled != (boundary == proto.KeyEnter) || snap.Commit != tc.typed {
					t.Fatalf("%q implicitly corrected spelling: %+v", boundary, snap)
				}
			}
		})
	}
}

func TestEnglishAutocompleteSpellingCorrectionCanBeUndone(t *testing.T) {
	for _, undo := range []string{proto.KeyEscape, proto.KeyBackspace} {
		s := autocompleteSession()
		s.English = english.Prototype()
		mixedType(s, "recieve")
		key(s, proto.KeyTab)
		if snap := key(s, undo); snap.Text() != "recieve" {
			t.Fatalf("%s did not restore spelling: %+v", undo, snap)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != "recieve" || !snap.Handled {
			t.Fatal(snap)
		}
	}
}

func TestEnglishAutocompleteReservesCorrectionCandidates(t *testing.T) {
	s := autocompleteSession()
	s.English = english.New([]english.Word{
		{Text: "teha", Weight: 100}, {Text: "tehb", Weight: 90}, {Text: "tehc", Weight: 80},
		{Text: "tehd", Weight: 70}, {Text: "tehe", Weight: 60}, {Text: "tehf", Weight: 50},
		{Text: "the", Weight: 40}, {Text: "ten", Weight: 30}, {Text: "tea", Weight: 20},
	})
	snap := mixedType(s, "teh")
	if snap.Candidates == nil || len(snap.Candidates.Items) != 6 || snap.Candidates.Items[0].Text != "teha" {
		t.Fatalf("completion order or candidate bound changed: %+v", snap)
	}
	seen := map[string]bool{}
	for _, c := range snap.Candidates.Items {
		if seen[strings.ToLower(c.Text)] {
			t.Fatalf("duplicate suggestion: %+v", snap.Candidates)
		}
		seen[strings.ToLower(c.Text)] = true
	}
	if !seen["the"] {
		t.Fatal("completions hid correction", snap.Candidates)
	}
	pickText(t, s, "the")
	if snap := key(s, proto.KeyEnter); snap.Commit != "the" {
		t.Fatal(snap)
	}
}

func TestEnglishAutocompleteKnownSpellingIsProtected(t *testing.T) {
	s := autocompleteSession()
	s.English = english.Prototype()
	for _, word := range []string{"from", "hello"} {
		mixedType(s, word)
		if snap := key(s, proto.KeyTab); snap.Handled || snap.Commit != word {
			t.Fatalf("valid word was corrected: %+v", snap)
		}
	}
	s.model.Store.English["recieve"] = personal.EnglishEvidence{
		Text: "recieve", Confirmed: 10, Days: 5, Updated: s.model.Store.Time,
	}
	mixedType(s, "recieve")
	if snap := key(s, proto.KeyTab); snap.Handled || snap.Commit != "recieve" {
		t.Fatalf("confirmed personal spelling was corrected: %+v", snap)
	}
}

func TestEnglishAutocompleteCorrectionDoesNotConfirmLearning(t *testing.T) {
	s := autocompleteSession()
	s.English = english.Prototype()
	s.MixedInput = true
	s.model.AdvanceTime(100 * englishDay)
	mixedType(s, "recieve")
	key(s, proto.KeyTab)
	mixedType(s, "sx")
	key(s, proto.KeyBackspace)
	if snap := key(s, proto.KeyEnter); snap.Commit != "receives" || !snap.Handled || len(s.model.Store.English) != 0 {
		t.Fatalf("correction learned as independently typed: %+v %+v", snap, s.model.Store.English)
	}
}
