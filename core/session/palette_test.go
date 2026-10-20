package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/emoji"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/punct"
)

func TestPaletteStableSelectionAcrossRecentChangesAndPartialPage(t *testing.T) {
	s := paletteSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=")
	chosen := emoji.ID("😂")
	s.model.RememberEmoji("😂")
	if snap := s.Handle(proto.Request{Type: proto.Select, Index: chosen}); snap.Text() != "我😂" {
		t.Fatalf("open selection changed: %q", snap.Text())
	}
	r := New(s.model)
	r.EmojiShortcut = "equals"
	r.CandidateColumns = 5
	typeSyl(r, "ㄨㄛˇ")
	key(r, "=")
	if snap := r.Handle(proto.Request{Type: proto.Select, Index: chosen}); snap.Text() != "我😂" {
		t.Fatalf("replay selection changed: %q", snap.Text())
	}
	x := paletteSession()
	x.model.RememberEmoji("😅")
	key(x, "=")
	x.cand.items = x.cand.items[:2]
	x.cand.ids = x.cand.ids[:2]
	if snap := key(x, "6"); snap.Commit != "" || snap.Candidates == nil || len(x.model.Store.EmojiRecent) != 1 {
		t.Fatal("empty grid slot selected a stable ID")
	}
}

func paletteSession() *Session {
	s := newSession()
	s.EmojiShortcut = "equals"
	s.EmojiEqualsFirst = false // exercise the emoji-only layout as well.
	s.CandidateColumns = 5
	return s
}

func TestPaletteDirectSelectionAndEscapes(t *testing.T) {
	s := paletteSession()
	s.Punct = punctFunc(func(string) ([]punct.Edit, error) {
		t.Fatal("direct palette choice invoked punctuation")
		return nil, nil
	})
	if snap := key(s, "="); snap.Candidates == nil || snap.Candidates.Columns != 5 || !s.Composing() {
		t.Fatalf("palette: %+v", snap)
	}
	snap := key(s, "1")
	if snap.Commit != "😀" || snap.Text() != "" || snap.Candidates != nil || !snap.PersonalChanged {
		t.Fatalf("selection: %+v", snap)
	}
	if len(s.model.Store.EmojiRecent) != 1 || s.model.Store.Total != 0 || len(s.model.Store.English) != 0 {
		t.Fatal("palette order polluted word learning")
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "" {
		t.Fatal("duplicate commit")
	}
	key(s, "=")
	if snap := key(s, "="); snap.Commit != "=" || snap.Text() != "" || snap.Candidates != nil || s.Composing() || len(s.model.Store.EmojiRecent) != 1 {
		t.Fatalf("escape: %+v", snap)
	}
	key(s, "=")
	if snap := key(s, proto.KeyEscape); snap.Commit != "" || snap.Candidates != nil || s.Composing() {
		t.Fatalf("cancel: %+v", snap)
	}
	s.EmojiShortcut = "off"
	if snap := key(s, "="); snap.Handled || snap.Candidates != nil {
		t.Fatal("disabled trigger captured input")
	}
	s.EmojiShortcut = "equals"
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, "="); snap.Handled || snap.Candidates != nil {
		t.Fatal("English trigger captured input")
	}
}

func TestPaletteDoesNotCaptureMixedURLsOrLearnKaomojiWords(t *testing.T) {
	s := paletteSession()
	s.MixedInput = true
	text := "https://example.test/?a=b"
	for _, r := range text {
		if snap := key(s, string(r)); snap.Candidates != nil {
			t.Fatalf("URL opened palette at %q", r)
		}
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != text {
		t.Fatalf("URL=%q", snap.Commit)
	}
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=")
	key(s, proto.KeyTab)
	key(s, "1")
	if snap := key(s, "="); snap.Candidates == nil || snap.Candidates.Kind != "kaomoji" {
		t.Fatal("kaomoji insertion prevented opening another palette（= 開在上次選用的顏文字分頁，#124）")
	}
	key(s, proto.KeyEscape)
	if snap := key(s, proto.KeyEnter); snap.Commit != "我(^_^)" || len(s.model.Store.English) != 0 {
		t.Fatalf("face/English pollution: %+v", snap)
	}
	for _, word := range []string{"(^_^)", "T_T", "_"} {
		if s.model.Store.Uni[word] != 0 {
			t.Fatal("face learned as text")
		}
	}
}

func TestPalettePreservesPendingAndGridNavigation(t *testing.T) {
	s := paletteSession()
	key(s, "r") // unfinished ㄐ
	before := s.snapshot()
	key(s, "=")
	key(s, proto.KeyDown)
	if got := s.snapshot().Candidates.Highlight; got != 5 {
		t.Fatalf("down highlight=%d", got)
	}
	key(s, proto.KeyRight)
	if got := s.snapshot().Candidates.Highlight; got != 6 {
		t.Fatalf("right highlight=%d", got)
	}
	key(s, proto.KeyEscape)
	if got := s.snapshot(); got.Text() != before.Text() || got.Cursor != before.Cursor {
		t.Fatalf("cancel changed pending: %+v", got)
	}
	key(s, "u")
	key(s, "3") // ㄐㄧˇ not in fixture; preserve visible input nonetheless
	key(s, "=")
	snap := key(s, "1")
	if snap.Commit != "" || snap.Text() == "😀" {
		t.Fatalf("selection replaced composition: %+v", snap)
	}
}

func TestPaletteDefaultEqualsChoiceAndOptOut(t *testing.T) {
	for _, columns := range []int{1, 5} {
		for _, first := range []bool{true, false} {
			s := newSession()
			if s.EmojiShortcut != "equals" || !s.EmojiEqualsFirst {
				t.Fatal("palette defaults must enable equals and the first literal choice")
			}
			s.CandidateColumns, s.EmojiEqualsFirst = columns, first
			s.model.RememberEmoji("😂")
			snap := key(s, "=")
			want := "😂"
			if first {
				want = "="
			}
			if snap.Candidates == nil || snap.Candidates.Items[0].Text != want {
				t.Fatalf("first=%v columns=%d: %+v", first, columns, snap)
			}
			if first {
				if snap.Candidates.Items[0].ID != emoji.EqualsID || snap.Candidates.Items[1].Text != "😂" {
					t.Fatalf("equals displaced recent ordering or ID: %+v", snap)
				}
				if snap = s.Handle(proto.Request{Type: proto.Select, Index: emoji.EqualsID}); snap.Commit != "=" || snap.PersonalChanged {
					t.Fatalf("literal choice: %+v", snap)
				}
				key(s, "=")
			}
			if snap = key(s, "="); snap.Commit != "=" || snap.PersonalChanged || snap.Candidates != nil {
				t.Fatalf("double equals first=%v: %+v", first, snap)
			}
			if len(s.model.Store.EmojiRecent) != 1 || s.model.Store.EmojiRecent[0] != "😂" {
				t.Fatal("literal equals learned as recent emoji")
			}
			key(s, "=")
			if snap = key(s, proto.KeyTab); snap.Candidates.Items[0].Text == "=" {
				t.Fatal("literal option belongs only in the emoji palette")
			}
			key(s, proto.KeyEscape)
			key(s, "r")
			key(s, "=")
			if snap = key(s, "="); snap.Text() != "ㄐ=" || snap.Commit != "" || snap.PersonalChanged {
				t.Fatalf("double equals lost pending composition: %+v", snap)
			}
		}
	}
}

func TestEmojiGraphemeEditingAndOffsets(t *testing.T) {
	for _, text := range []string{"👨‍👩‍👧‍👦", "👍🏽", "🇹🇼", "❤️", "1️⃣", "𠮷"} {
		t.Run(text, func(t *testing.T) {
			s := newSession()
			typeSyl(s, "ㄨㄛˇ")
			s.insertLiteral(text)
			s.refresh()
			after := s.snapshot()
			if text == "👨‍👩‍👧‍👦" && after.Cursor.UTF16 != 12 {
				t.Fatalf("UTF16=%d", after.Cursor.UTF16)
			}
			if snap := key(s, proto.KeyLeft); snap.Cursor.Rune != 1 {
				t.Fatalf("split cluster: %+v", snap.Cursor)
			}
			key(s, proto.KeyRight)
			if snap := key(s, proto.KeyBackspace); snap.Text() != "我" {
				t.Fatalf("backspace=%q", snap.Text())
			}
			s.insertLiteral(text)
			s.refresh()
			key(s, proto.KeyLeft)
			if snap := key(s, proto.KeyDelete); snap.Text() != "我" {
				t.Fatalf("delete=%q", snap.Text())
			}
		})
	}
}

func TestKaomojiUndoAndPrivateOrder(t *testing.T) {
	s := paletteSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "=")
	key(s, proto.KeyTab)
	if snap := key(s, "1"); snap.Text() != "我(^_^)" || snap.Candidates != nil {
		t.Fatalf("kaomoji: %+v", snap)
	}
	if snap := key(s, proto.KeyBackspace); snap.Text() != "我" {
		t.Fatalf("undo=%q", snap.Text())
	}
	key(s, "=") // 上次選的是顏文字：直接開在顏文字分頁（#124）
	key(s, "1")
	key(s, proto.KeyLeft)
	if snap := key(s, proto.KeyBackspace); snap.Text() != "我(^_)" {
		t.Fatalf("edited face=%q", snap.Text())
	}

	for _, private := range []bool{false, true} {
		x := paletteSession()
		x.paused = !private
		if private {
			x.localBlocked = true
		}
		key(x, "=")
		snap := key(x, "1")
		if len(x.model.Store.EmojiRecent) != 0 || snap.PersonalChanged {
			t.Fatal("protected selection recorded")
		}
	}
}

func TestPalettePlatformLayoutAndLiteralReselection(t *testing.T) {
	s := paletteSession()
	s.Handle(proto.Request{Type: proto.Display, Columns: 1})
	key(s, "=")
	if s.snapshot().Candidates.Columns != 0 {
		t.Fatal("native display ignored")
	}
	key(s, proto.KeyDown)
	if s.snapshot().Candidates.Highlight != 1 {
		t.Fatal("native navigation mismatch")
	}
	key(s, proto.KeyEscape)
	typeSyl(s, "ㄨㄛˇ")
	s.insertLiteral("👨‍👩‍👧‍👦")
	s.refresh()
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	if s.cursor != 1 {
		t.Fatal("cursor inside family cluster")
	}
	key(s, proto.KeyDown)
	if snap := key(s, "1"); snap.Text() != "我👨‍👩‍👧‍👦" {
		t.Fatalf("reselection split emoji: %q", snap.Text())
	}
}
