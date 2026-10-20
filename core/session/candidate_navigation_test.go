package session

import (
	"fmt"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestGridCrossesPagesInSameColumn(t *testing.T) {
	for _, cols := range []int{2, 3, 5} {
		for _, tc := range []struct {
			count, at int
			key       string
			want      int
		}{
			{24, 9 / cols * cols, proto.KeyDown, 10},
			{24, 11, proto.KeyUp, min(9/cols*cols+1, 9)},
			{12, 9, proto.KeyDown, min(10+9%cols, 11)}, // missing column clamps to the last item
		} {
			t.Run(fmt.Sprintf("%d/%d/%s", cols, tc.at, tc.key), func(t *testing.T) {
				s := newSession()
				s.CandidateColumns = cols
				items := make([]string, tc.count)
				for i := range items {
					items[i] = fmt.Sprint(i)
				}
				s.openSymbols(items, 0, 0, false)
				s.cand.cursor = tc.at
				key(s, tc.key)
				if s.cand.cursor != tc.want {
					t.Fatalf("cursor=%d, want %d", s.cand.cursor, tc.want)
				}
			})
		}
	}
}

func TestGridWordLengthBoundariesPreserveAnchor(t *testing.T) {
	for _, before := range []bool{false, true} {
		s := cursorSession()
		s.CandidateColumns, s.CandidateBeforeCursor = 5, before
		typeSyl(s, "ㄈㄥ")
		typeSyl(s, "ㄐㄧㄥˇ")
		if !before {
			key(s, proto.KeyLeft)
			key(s, proto.KeyLeft)
		}
		snap := key(s, proto.KeyDown)
		cursorRange(t, snap, 0, 2)
		// The single characters of the caret's character share the grid (#161):
		// moving onto one narrows the range to that character.
		single := 0
		if before {
			single = 1
		}
		for range s.cand.split {
			snap = key(s, proto.KeyRight)
		}
		cursorRange(t, snap, single, 1)
		// One row holds every candidate and there is no other multi-character
		// length: down wraps to the first word, up lands on the last word.
		snap = key(s, proto.KeyDown)
		cursorRange(t, snap, 0, 2)
		if snap.Candidates.Highlight != 0 {
			t.Fatal("wrapping must start at the first candidate")
		}
		snap = key(s, proto.KeyUp)
		cursorRange(t, snap, 0, 2)
		if s.cand.cursor != s.cand.split-1 {
			t.Fatal("previous length must end at its last word, not its single characters")
		}
	}
}

func TestGridPaletteWrapsAndHorizontalKeysStayInRow(t *testing.T) {
	for _, kind := range []string{"emoji", "kaomoji"} {
		s := paletteSession()
		s.openEmoji(kind)
		s.cand.cursor = len(s.cand.items) - 1
		key(s, proto.KeyDown)
		if s.cand.cursor != 0 {
			t.Fatal("palette must wrap to its first item")
		}
		key(s, proto.KeyLeft)
		if s.cand.cursor != 0 {
			t.Fatal("left must stay in row")
		}
		key(s, proto.KeyUp)
		if s.cand.cursor != len(s.cand.items)-1 {
			t.Fatal("palette must wrap to its last item")
		}
		s.cand.cursor = s.candidateColumns() - 1
		key(s, proto.KeyRight)
		if s.cand.cursor != s.candidateColumns()-1 {
			t.Fatal("right must stay in row")
		}
	}
}

func TestCandidateTitleNamesReplacementAndWordLengthShortcut(t *testing.T) {
	for _, columns := range []int{1, 5} {
		s := cursorSession()
		s.CandidateColumns = columns
		typeSyl(s, "ㄈㄥ")
		typeSyl(s, "ㄐㄧㄥˇ")
		key(s, proto.KeyLeft)
		key(s, proto.KeyLeft)
		cursorChoose(t, s, key(s, proto.KeyDown), "風景")
		first := key(s, proto.KeyDown)
		if first.Candidates.Title != "修改「風景」 · Tab 切換詞長" {
			t.Fatalf("whole-word heading: %q", first.Candidates.Title)
		}
		single := key(s, proto.KeyTab)
		if single.Candidates.Title != "修改「風」 · Tab 切換詞長" {
			t.Fatalf("single-character heading: %q", single.Candidates.Title)
		}
		selected := cursorChoose(t, s, single, "風")
		if selected.Text() != first.Text() || selected.Commit != "" {
			t.Fatal("showing range must not change the composition")
		}
	}
}

func TestCandidateTitleAfterRawPhoneticPrefix(t *testing.T) {
	s := cursorSession()
	s.CandidateBeforeCursor = true
	// A missing reading may reach the fallback through restored/edited composition.
	s.insert(zhuyin.Syllable("ㄗㄨˇ")) // one syllable displays several runes
	s.insert(zhuyin.Syllable("ㄈㄥ"))
	snap := key(s, proto.KeyDown)
	cursorRange(t, snap, 1, 1)
	if snap.Candidates.Title != "修改「ㄈㄥ」 · Tab 切換詞長" {
		t.Fatalf("range after multi-rune phonetic prefix: %q (preedit %q)", snap.Candidates.Title, snap.Text())
	}
}
