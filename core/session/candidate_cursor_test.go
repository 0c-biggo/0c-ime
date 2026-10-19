package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

func cursorSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct{ word, reading string }{
		{"他", "ㄊㄚ"}, {"她", "ㄊㄚ"}, {"它", "ㄊㄚ"},
		{"好", "ㄏㄠˇ"}, {"郝", "ㄏㄠˇ"}, {"我", "ㄨㄛˇ"},
		{"風", "ㄈㄥ"}, {"景", "ㄐㄧㄥˇ"}, {"井", "ㄐㄧㄥˇ"},
		{"風景", "ㄈㄥ ㄐㄧㄥˇ"}, {"風井", "ㄈㄥ ㄐㄧㄥˇ"},
	} {
		lex.Add(lexicon.Entry{Surface: e.word, Reading: zhuyin.ParseReading(e.reading), PRW: 1})
		sys.SetUni(e.word, .001)
	}
	return New(personal.NewModel(lex, sys))
}

func cursorChoose(t *testing.T, s *Session, snap proto.Snapshot, text string) proto.Snapshot {
	t.Helper()
	if snap.Candidates != nil {
		for _, it := range snap.Candidates.Items {
			if it.Text == text {
				return s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
			}
		}
	}
	t.Fatalf("missing %q: %+v", text, snap.Candidates)
	return snap
}

func cursorRange(t *testing.T, snap proto.Snapshot, start, length int) {
	t.Helper()
	if c := snap.Candidates; c == nil || c.Start != start || c.Length != length {
		t.Fatalf("range want %d/%d, got %+v", start, length, c)
	}
}

func TestCandidateCursorSingleCharacterDirectionAndEdges(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, at := range []int{0, 1, 2} {
			s := cursorSession()
			s.CandidateBeforeCursor = before
			typeSyl(s, "ㄊㄚ")
			typeSyl(s, "ㄏㄠˇ")
			key(s, proto.KeyLeft)
			key(s, proto.KeyLeft)
			for range at {
				key(s, proto.KeyRight)
			}
			snap := key(s, proto.KeyDown)
			start := min(at, 1)
			if before {
				start = max(0, at-1)
			}
			cursorRange(t, snap, start, 1)
			want, chosen := "她好", "她"
			if start == 1 {
				want, chosen = "他郝", "郝"
			}
			snap = cursorChoose(t, s, snap, chosen)
			if snap.Text() != want || snap.Cursor.Rune != at || snap.Commit != "" {
				t.Fatalf("before=%v at=%d: %+v", before, at, snap)
			}
			if key(s, proto.KeyEnter).Commit != want {
				t.Fatal("commit changed selected text")
			}
		}
	}
}

func TestCandidateBeforeCursorWholeWordAndLengthAnchor(t *testing.T) {
	s := cursorSession()
	for _, syl := range []string{"ㄨㄛˇ", "ㄈㄥ", "ㄐㄧㄥˇ", "ㄏㄠˇ"} {
		typeSyl(s, syl)
	}
	// Lock the fixture's segmentation explicitly, without depending on scores.
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	cursorChoose(t, s, s.Handle(proto.Request{Type: proto.Candidates, Length: 2}), "風景")
	s.CandidateBeforeCursor = true
	s.Handle(proto.Request{Type: proto.Cursor, To: 3})
	snap := key(s, proto.KeyDown)
	cursorRange(t, snap, 1, 2)
	// Tab wraps to the single character immediately before the original caret.
	snap = key(s, proto.KeyTab)
	cursorRange(t, snap, 2, 1)
	// Walking beyond the first single-character candidate returns to the word,
	// keeping its right edge at the caret instead of extending into 好.
	snap = key(s, proto.KeyUp)
	cursorRange(t, snap, 1, 2)
	snap = cursorChoose(t, s, snap, "風井")
	if snap.Text() != "我風井好" || snap.Cursor.Rune != 3 {
		t.Fatalf("%+v", snap)
	}
	// Continue at the unchanged caret: the new character belongs before 好.
	snap = typeSyl(s, "ㄨㄛˇ")
	if snap.Text() != "我風井我好" || snap.Cursor.Rune != 4 {
		t.Fatalf("%+v", snap)
	}
}

func TestCandidateBeforeCursorInsideWordAndCancel(t *testing.T) {
	s := cursorSession()
	typeSyl(s, "ㄈㄥ")
	typeSyl(s, "ㄐㄧㄥˇ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	cursorChoose(t, s, s.Handle(proto.Request{Type: proto.Candidates, Length: 2}), "風景")
	s.CandidateBeforeCursor = true
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	before := s.snapshot()
	snap := key(s, " ")
	cursorRange(t, snap, 0, 1)
	snap = key(s, proto.KeyEscape)
	if snap.Candidates != nil || snap.Text() != before.Text() || snap.Cursor != before.Cursor {
		t.Fatalf("cancel altered composition: %+v", snap)
	}
}

func TestCandidateBeforeCursorLiteralAndMixedBoundaries(t *testing.T) {
	for _, literal := range []string{"，", "hello", "👨‍👩‍👧‍👦"} {
		s := cursorSession()
		s.CandidateBeforeCursor = true
		typeSyl(s, "ㄊㄚ")
		s.insertLiteral(literal)
		typeSyl(s, "ㄏㄠˇ")
		snap := key(s, proto.KeyDown)
		cursorRange(t, snap, len(s.syls)-1, 1)
		for range 5 {
			snap = key(s, proto.KeyTab)
			cursorRange(t, snap, len(s.syls)-1, 1)
		}
		snap = cursorChoose(t, s, snap, "郝")
		if snap.Text() != "他"+literal+"郝" {
			t.Fatalf("%+v", snap)
		}
		key(s, proto.KeyLeft)
		snap = key(s, proto.KeyDown)
		start, length := len(s.syls)-2, 1
		if literal == "👨‍👩‍👧‍👦" {
			start, length = 1, len([]rune(literal))
		}
		if snap.Candidates == nil || snap.Candidates.Start != start || snap.Candidates.Length != length {
			t.Fatalf("literal grapheme range: %+v", snap.Candidates)
		}
	}
	// Pending mixed keys still open their own spelling candidates, even when
	// the Chinese reselection direction is before the cursor.
	s := mixedSession()
	s.CandidateBeforeCursor = true
	for _, k := range "hello" {
		key(s, string(k))
	}
	snap := key(s, proto.KeyDown)
	if snap.Candidates == nil || !s.cand.mixed {
		t.Fatalf("%+v", snap)
	}
}

// 後方模式（Linux／Windows 預設）的游標在組字尾端時，後面沒有字可選；改選結束在游標的詞，
// 換詞長也以游標為詞尾。打完一句回頭改最後一個詞是常見操作，不能只改最後一個字。
func TestCandidateAfterCursorAtTailSelectsWordEndingAtCaret(t *testing.T) {
	s := cursorSession()
	for _, syl := range []string{"ㄨㄛˇ", "ㄈㄥ", "ㄐㄧㄥˇ"} {
		typeSyl(s, syl)
	}
	if s.CandidateBeforeCursor {
		t.Fatal("fixture must use the after-cursor default")
	}
	snap := key(s, proto.KeyDown)
	cursorRange(t, snap, 1, 2)
	if snap.Text() != "我風景" || snap.Cursor.Rune != 3 {
		t.Fatalf("opening changed composition: %+v", snap)
	}
	// Tab 由整詞換到游標前的單字，再回到整詞；右緣固定在游標。
	cursorRange(t, key(s, proto.KeyTab), 2, 1)
	cursorRange(t, key(s, proto.KeyTab), 1, 2)
	// 詞的候選後面接著列游標前那個字的單字（#161）：往下走進單字，範圍換成那個字，往上回到詞。
	for range s.cand.split {
		snap = key(s, proto.KeyDown)
	}
	cursorRange(t, snap, 2, 1)
	snap = key(s, proto.KeyUp)
	cursorRange(t, snap, 1, 2)
	// 走過清單尾端：單字已經列過，沒有其他多字詞長，回到第一個詞。
	key(s, proto.KeyEnd)
	snap = key(s, proto.KeyDown)
	cursorRange(t, snap, 1, 2)
	if snap.Candidates.Highlight != 0 {
		t.Fatalf("past the end must wrap to the first word: %+v", snap.Candidates)
	}
	key(s, proto.KeyDown) // 回到原本反白的整詞，下面照舊改選風井
	snap = key(s, proto.KeyUp)
	snap = cursorChoose(t, s, snap, "風井")
	if snap.Text() != "我風井" || snap.Cursor.Rune != 3 || snap.Candidates != nil {
		t.Fatalf("%+v", snap)
	}
	// 游標留在句尾，接著打字接在選好的詞後面。
	snap = typeSyl(s, "ㄏㄠˇ")
	if snap.Text() != "我風井好" || snap.Cursor.Rune != 4 {
		t.Fatalf("%+v", snap)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "我風井好" {
		t.Fatalf("commit %q", got)
	}
	// 手選整詞照常學習：記下這個讀音的選擇，下一次直接打出來。
	if !s.model.Store.Readings["風井\t"+zhuyin.Key(zhuyin.ParseReading("ㄈㄥ ㄐㄧㄥˇ"))] {
		t.Fatalf("whole-word pick not learned: %v", s.model.Store.Readings)
	}
	s = New(s.model)
	typeSyl(s, "ㄈㄥ")
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "風井" {
		t.Fatalf("learned pick not reused: %q", got)
	}
}

// 句尾以外的位置維持後方模式：選游標所在的字或由游標開始的詞。
func TestCandidateAfterCursorAwayFromTailUnchanged(t *testing.T) {
	for _, tc := range []struct{ at, start, length int }{{0, 0, 1}, {1, 1, 2}, {2, 2, 1}} {
		s := cursorSession()
		for _, syl := range []string{"ㄨㄛˇ", "ㄈㄥ", "ㄐㄧㄥˇ"} {
			typeSyl(s, syl)
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: tc.at})
		cursorRange(t, key(s, proto.KeyDown), tc.start, tc.length)
		if s.cand.before {
			t.Fatalf("at=%d opened before the cursor", tc.at)
		}
	}
}

// 句尾是標點、英文或接在它們後面的中文時，開窗不跨越原樣片段。
func TestCandidateAfterCursorAtTailLiteralBoundaries(t *testing.T) {
	// 句尾符號仍開符號替換清單。
	s := cursorSession()
	typeSyl(s, "ㄊㄚ")
	s.insertLiteral("，")
	snap := key(s, proto.KeyDown)
	cursorRange(t, snap, 1, 1)
	if !s.cand.literal || len(snap.Candidates.Items) < 2 {
		t.Fatalf("tail punctuation should open symbol alternatives: %+v", snap.Candidates)
	}
	alt := snap.Candidates.Items[1]
	snap = s.Handle(proto.Request{Type: proto.Select, Index: alt.ID})
	if snap.Text() != "他"+alt.Text || snap.Cursor.Rune != 2 {
		t.Fatalf("symbol replacement: %+v", snap)
	}

	// 標點後的整詞：只往左延伸到標點為止。
	s = cursorSession()
	typeSyl(s, "ㄊㄚ")
	s.insertLiteral("，")
	typeSyl(s, "ㄈㄥ")
	typeSyl(s, "ㄐㄧㄥˇ")
	cursorRange(t, key(s, proto.KeyDown), 2, 2)
	for range 4 {
		c := key(s, proto.KeyTab).Candidates
		if c == nil || c.Start < 2 || c.Start+c.Length != 4 {
			t.Fatalf("Tab crossed the literal: %+v", c)
		}
	}

	// 中英混打：未完成的英文仍開拼字候選；英文後的中文在句尾選自己的詞，不碰英文。
	m := mixedSession()
	mixedType(m, "hello")
	if key(m, proto.KeyDown); m.cand == nil || !m.cand.mixed {
		t.Fatalf("pending English: %+v", m.cand)
	}
	m = mixedSession()
	if got := mixedType(m, "ji3hellot ").Text(); got != "我hello吃" {
		t.Fatalf("fixture: %q", got)
	}
	end := len(m.syls)
	cursorRange(t, key(m, proto.KeyDown), end-1, 1)
	for range 3 {
		c := key(m, proto.KeyTab).Candidates
		if c == nil || c.Start != end-1 || c.Length != 1 {
			t.Fatalf("Tab crossed English: %+v", c)
		}
	}
}
