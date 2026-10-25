package session

import (
	"reflect"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/proto"
)

func TestOriginalStylePreservesLegacySequences(t *testing.T) {
	for _, sequence := range [][]string{
		{"j", "i", "3", "<", proto.KeyDown, " ", proto.KeyEnter, proto.KeyEnter},
		{"w", "u", "/", "3", proto.KeyTab, proto.KeyBackTab, proto.KeyEscape},
		{"`", proto.KeyPageDown, proto.KeyEnter, proto.KeyEnter},
	} {
		a, b := styleSession("0c"), styleSession("0c")
		b.InputStyle = inputstyle.Rules{}
		for _, k := range sequence {
			sa, sb := key(a, k), key(b, k)
			if !reflect.DeepEqual(sa, sb) {
				t.Fatalf("0c changed at %q: %+v != %+v", k, sa, sb)
			}
		}
	}
}

func TestMcBopomofoCycleInSecondWordAndCaps(t *testing.T) {
	s := styleSession("mcbopomofo")
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄐㄧㄥˇ")
	if a := key(s, proto.KeyTab); a.Text() != "我井" {
		t.Fatalf("second word cycle: %+v", a)
	}
	key(s, proto.KeyEnter)
	s.Handle(proto.Request{Type: proto.CapsLock, On: true})
	if a := key(s, "a"); !a.Handled || a.Commit != "a" {
		t.Fatalf("shell-translated lowercase must be committed: %+v", a)
	}
	if a := key(s, "A"); !a.Handled || a.Commit != "A" {
		t.Fatalf("shell-normalized Caps+Shift must commit uppercase: %+v", a)
	}
}

func TestCustomCandidateSelectionAndCommit(t *testing.T) {
	s := styleSession("0c")
	s.InputStyle.CandidateSpace = "select"
	s.InputStyle.CandidateEnter = "commit"
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyDown)
	key(s, proto.KeyDown)
	if a := key(s, " "); a.Text() != "井" || a.Candidates != nil || a.Commit != "" {
		t.Fatalf("Space select: %+v", a)
	}
	key(s, proto.KeyDown)
	if a := key(s, proto.KeyEnter); a.Commit == "" || a.Text() != "" {
		t.Fatalf("Enter select and commit: %+v", a)
	}
}

func styleSession(id string) *Session {
	s := newSession()
	p, ok := inputstyle.Get(id)
	if !ok {
		panic(id)
	}
	s.InputStyle = p.Rules
	s.CandidateBeforeCursor = p.Behavior.CandidateCursor == "before"
	s.EscKeepComposition = !p.Behavior.EscClear
	s.SpaceInsertsSpace = !p.Behavior.SpaceSelects
	s.AdvanceAfterSelect = p.Behavior.AdvanceAfterSelect
	s.SelectionKeys = p.Behavior.SelectionKeys
	s.CapsLockEnglish = p.Behavior.CapsLockEnglish
	s.CtrlPunct = p.Behavior.CtrlPunctuation
	s.EmojiShortcut = p.Behavior.EmojiShortcut
	return s
}

func TestMicrosoftWidthIsStateAndIncludesEnglish(t *testing.T) {
	s := styleSession("microsoft")
	if a := key(s, proto.KeyShiftSpace); !a.FullWidth || a.Text() != "" || a.Commit != "" {
		t.Fatalf("toggle: %+v", a)
	}
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	for _, c := range []struct{ k, w string }{{"a", "ａ"}, {"A", "Ａ"}, {"1", "１"}, {" ", "　"}} {
		if a := key(s, c.k); !a.Handled || a.Commit != c.w {
			t.Fatalf("%q: %+v", c.k, a)
		}
	}
	if a := key(s, proto.KeyShiftSpace); a.FullWidth {
		t.Fatal("width did not toggle back")
	}
	if a := key(s, "a"); a.Handled {
		t.Fatal("halfwidth idle English should pass through")
	}
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	typeSyl(s, "ㄨㄛˇ")
	if a := key(s, proto.KeyShiftSpace); a.Text() != "我" || a.Commit != "" {
		t.Fatalf("toggle touched composition: %+v", a)
	}
	if a := key(s, proto.KeyEnter); a.Commit != "我" || !a.FullWidth {
		t.Fatalf("commit should retain width: %+v", a)
	}
}

func TestMicrosoftPrefixAndInlineUp(t *testing.T) {
	s := styleSession("microsoft")
	if a := key(s, "`"); a.Text() != "`" || a.Candidates != nil {
		t.Fatalf("prefix: %+v", a)
	}
	if a := key(s, ","); a.Text() != "，" {
		t.Fatalf("prefixed punctuation: %+v", a)
	}
	key(s, proto.KeyEnter)
	typeSyl(s, "ㄐㄧㄥˇ")
	if a := key(s, proto.KeyUp); a.Text() != "井" || a.Candidates != nil || a.Commit != "" {
		t.Fatalf("inline Up: %+v", a)
	}
	key(s, proto.KeyDown)
	if a := key(s, proto.KeyTab); a.Candidates == nil || a.Candidates.Columns != 5 {
		t.Fatalf("expand grid: %+v", a)
	}
}

func TestMicrosoftPrefixCancellationPreservesComposition(t *testing.T) {
	s := styleSession("microsoft")
	typeSyl(s, "ㄨㄛˇ")
	key(s, "`")
	if a := key(s, proto.KeyEscape); a.Text() != "我" || a.Commit != "" {
		t.Fatalf("Escape must cancel just the prefix: %+v", a)
	}
	key(s, "`")
	if a := key(s, proto.KeyEnter); a.Commit != "我`" || a.Text() != "" {
		t.Fatalf("Enter must preserve the displayed raw prefix: %+v", a)
	}
	key(s, "`")
	// Navigation does not reinterpret a named key as a printable symbol.
	if a := key(s, proto.KeyLeft); a.Text() != "`" {
		t.Fatalf("named key discarded prefix: %+v", a)
	}
	if a := key(s, ","); a.Text() != "，" {
		t.Fatalf("prefix was not consumed once: %+v", a)
	}
	key(s, proto.KeyEnter)
	if a := key(s, ","); a.Text() != "ㄝ" {
		t.Fatalf("prefix survived commit: %+v", a)
	}
}

func TestMicrosoftObservedRawAndPrefixPunctuation(t *testing.T) {
	for _, tc := range []struct{ key, wide, prefix string }{
		{"\\", "＼", "＼"}, {"'", "’", "、"}, {"\"", "＂", "＂"}, {"<", "＜", "＜"}, {">", "＞", "＞"}, {"?", "？", "？"},
	} {
		s := styleSession("microsoft")
		if a := key(s, tc.key); a.Handled || a.Commit != "" || a.Text() != "" {
			t.Fatalf("halfwidth idle %q must reach app: %+v", tc.key, a)
		}
		key(s, proto.KeyShiftSpace)
		if a := key(s, tc.key); a.Text() != tc.wide || a.Commit != "" {
			t.Fatalf("Chinese fullwidth %q: %+v", tc.key, a)
		}
		key(s, proto.KeyEscape)
		s.Handle(proto.Request{Type: proto.Mode, On: true})
		if a := key(s, tc.key); a.Text() != tc.wide || a.Commit != "" {
			t.Fatalf("English fullwidth %q: %+v", tc.key, a)
		}
		key(s, proto.KeyEscape)
		s.Handle(proto.Request{Type: proto.Mode, On: false})
		key(s, proto.KeyShiftSpace)
		key(s, "`")
		if a := key(s, tc.key); a.Text() != tc.prefix {
			t.Fatalf("prefix %q: %+v", tc.key, a)
		}
	}
}

func TestMicrosoftRawPunctuationInComposition(t *testing.T) {
	for _, r := range []string{"\\", "'", "<"} {
		s := styleSession("microsoft")
		key(s, "s")
		key(s, "u")
		if a := key(s, r); a.Commit != r || a.Text() != "" {
			t.Fatalf("unfinished reading + raw %q: %+v", r, a)
		}
		typeSyl(s, "ㄨㄛˇ")
		if a := key(s, r); a.Commit != "" || a.Text() != "我"+r {
			t.Fatalf("completed word + raw %q: %+v", r, a)
		}
		if a := key(s, proto.KeyEscape); a.Commit != "" || a.Text() != "" {
			t.Fatalf("Esc should clear marked raw punctuation: %+v", a)
		}
		typeSyl(s, "ㄨㄛˇ")
		key(s, proto.KeyDown)
		if a := key(s, r); a.Candidates == nil || a.Text() != "我" || a.Commit != "" {
			t.Fatalf("candidate raw must be ignored: %+v", a)
		}
	}
}

func TestStyleHotkeyClearsImmediatelyPreviousKeyState(t *testing.T) {
	s := styleSession("microsoft")
	key(s, "3")
	if s.pendingToneKey == nil {
		t.Fatal("fixture needs an idle tone key")
	}
	key(s, proto.KeyShiftSpace)
	if s.pendingToneKey != nil {
		t.Fatal("style shortcut left the previous tone key active")
	}
}

func TestStyleLiteralPreservesPendingEnglishOrder(t *testing.T) {
	for _, word := range []string{"h", "he"} {
		s := styleSession("microsoft")
		s.EnglishAutocomplete = true
		s.Handle(proto.Request{Type: proto.Mode, On: true})
		for _, r := range word {
			key(s, string(r))
		}
		key(s, proto.KeyShiftSpace)
		if a := key(s, "x"); a.Text() != word+"ｘ" || a.Commit != "" || a.Candidates != nil {
			t.Fatalf("%q + wide x reordered or accepted completion: %+v", word, a)
		}
		if a := key(s, proto.KeyEnter); a.Commit != word+"ｘ" {
			t.Fatalf("commit reordered text: %+v", a)
		}
	}
}

func TestStyleLiteralPreservesMixedRunAndShiftCommit(t *testing.T) {
	for _, tc := range []struct {
		style, key, suffix string
		width, commit      bool
	}{
		{"microsoft", "A", "Ａ", true, false},
		{"mcbopomofo", "\\", "＼", false, false},
		{"mcbopomofo", "A", "A", false, true},
	} {
		s := styleSession(tc.style)
		s.MixedInput = true
		var before proto.Snapshot
		for _, r := range "hello" {
			before = key(s, string(r))
		}
		if tc.width {
			key(s, proto.KeyShiftSpace)
		}
		a := key(s, tc.key)
		want := before.Text() + tc.suffix
		if tc.commit {
			if a.Commit != want || a.Text() != "" {
				t.Fatalf("Shift commit: got %+v want %q", a, want)
			}
		} else {
			if a.Text() != want || a.Commit != "" {
				t.Fatalf("style literal: got %+v want %q", a, want)
			}
			if a = key(s, proto.KeyEnter); a.Commit != want {
				t.Fatalf("commit: got %+v want %q", a, want)
			}
		}
	}
}

func TestMcBopomofoCycleAndShiftSpace(t *testing.T) {
	s := styleSession("mcbopomofo")
	typeSyl(s, "ㄐㄧㄥˇ")
	if a := key(s, proto.KeyTab); a.Text() != "井" || a.Candidates != nil || a.Cursor.Rune != 1 {
		t.Fatalf("next: %+v", a)
	}
	if a := key(s, proto.KeyBackTab); a.Text() != "景" || a.Candidates != nil {
		t.Fatalf("previous: %+v", a)
	}
	if a := key(s, proto.KeyEscape); a.Text() != "景" {
		t.Fatalf("Esc cleared completed word: %+v", a)
	}
	if a := key(s, proto.KeyShiftSpace); a.Commit != "景 " || a.FullWidth {
		t.Fatalf("ShiftSpace: %+v", a)
	}
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄔ")
	key(s, proto.KeyLeft)
	if a := key(s, proto.KeyShiftSpace); a.Text() != "我 吃" || a.Commit != "" {
		t.Fatalf("middle space: %+v", a)
	}
	if a := key(s, "A"); a.Commit != "我 吃A" || a.Text() != "" {
		t.Fatalf("ShiftLetter: %+v", a)
	}
	if a := key(s, "\\"); a.Text() != "＼" {
		t.Fatalf("backslash: %+v", a)
	}
	if a := key(s, proto.KeyTab); a.Text() != "／" {
		t.Fatalf("slash cycle: %+v", a)
	}
}

func TestMcBopomofoCycleReversesWithManyCandidates(t *testing.T) {
	s := manySession("1234567890")
	p, _ := inputstyle.Get("mcbopomofo")
	s.InputStyle = p.Rules
	s.CandidateBeforeCursor = true
	before := typeSyl(s, "ㄕˋ").Text()
	after := key(s, proto.KeyTab).Text()
	back := key(s, proto.KeyBackTab).Text()
	if before == after || before != back {
		t.Fatalf("before=%q tab=%q backtab=%q", before, after, back)
	}
	key(s, proto.KeyLeft)
	if s.styleCycle != nil {
		t.Fatal("cursor movement must invalidate the inline cycle")
	}
}

func TestChewingTabChangesSegmentation(t *testing.T) {
	s := styleSession("chewing")
	typeSyl(s, "ㄈㄥ")
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyHome)
	key(s, proto.KeyRight)
	if len(s.path) != 1 {
		t.Fatalf("fixture should be joined: %+v", s.path)
	}
	if a := key(s, proto.KeyTab); a.Text() != "風景" || a.Candidates != nil || len(s.path) != 2 {
		t.Fatalf("split: %+v path=%+v", a, s.path)
	}
	if a := key(s, proto.KeyTab); a.Text() != "風景" || len(s.path) != 1 {
		t.Fatalf("join: %+v path=%+v", a, s.path)
	}
	key(s, proto.KeyEnd)
	key(s, proto.KeyTab)
	if len(s.path) != 2 {
		t.Fatalf("end cycle: %+v", s.path)
	}
	key(s, proto.KeyTab)
	if len(s.path) != 1 {
		t.Fatalf("end cycle back: %+v", s.path)
	}
	if a := key(s, proto.KeyBackTab); a.Text() != "風景" || len(s.path) != 1 {
		t.Fatalf("ShiftTab is not reverse cycle: %+v", a)
	}
}

func TestChewingWidthAndHorizontalNavigation(t *testing.T) {
	s := styleSession("chewing")
	key(s, proto.KeyShiftSpace)
	if a := key(s, "["); a.Text() != "「" {
		t.Fatalf("Chinese punctuation unaffected by width: %+v", a)
	}
	key(s, proto.KeyEnter)
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if a := key(s, "["); a.Commit != "〔" {
		t.Fatalf("English width table: %+v", a)
	}
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyDown)
	if a := key(s, proto.KeyRight); a.Candidates == nil || a.Candidates.Highlight != 1 {
		t.Fatalf("horizontal right: %+v", a)
	}
	if a := key(s, proto.KeyEnter); a.Candidates != nil || a.Text() != "井" || a.Commit != "" {
		t.Fatalf("Enter selects only: %+v", a)
	}
}

func TestAppleShiftDeleteReopensTone(t *testing.T) {
	for _, deleteKey := range []string{proto.KeyShiftDelete, proto.KeyShiftBackspace} {
		s := styleSession("macos")
		typeSyl(s, "ㄐㄧㄥˇ")
		if a := key(s, deleteKey); a.Text() != "ㄐㄧㄥ" {
			t.Fatalf("%s remove tone: %+v", deleteKey, a)
		}
		if a := key(s, "3"); a.Text() != "景" {
			t.Fatalf("retype tone: %+v", a)
		}
	}
}

func TestStyleCandidatePageSizeAndSpace(t *testing.T) {
	s := manySession("1234567890")
	p, _ := inputstyle.Get("mcbopomofo")
	s.InputStyle = p.Rules
	typeSyl(s, "ㄕˋ")
	a := key(s, proto.KeyDown)
	if a.Candidates == nil {
		t.Fatal("no candidates")
	}
	if len(a.Candidates.Items) != 9 {
		t.Fatalf("page size: %+v", a.Candidates)
	}
	if a = key(s, "0"); a.Candidates == nil {
		t.Fatal("0 should not select tenth item in 9-item page")
	}
	if a = key(s, " "); a.Candidates.Page != 1 {
		t.Fatalf("Space pages: %+v", a)
	}
	if a = key(s, proto.KeyEnter); a.Candidates != nil || a.Commit != "" {
		t.Fatalf("Enter selects: %+v", a)
	}
}
