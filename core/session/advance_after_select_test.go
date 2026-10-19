package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #133：「選字後游標移到下一個字」關閉（預設）時游標留在原處；開啟時從候選窗選定後，
// 後方模式游標移到所選詞的結尾，前方模式往後移一個字（同新酷音 autoShiftCur），句尾時不動。
func TestAdvanceAfterSelectMovesCursorPastTheChoice(t *testing.T) {
	for _, tc := range []struct {
		name         string
		advance      bool
		before       bool
		at           int // 開候選前的游標
		chosen, text string
		cursor       int
		nextStart    int // 再按 ↓ 修改的位置；-1 不檢查
		nextLength   int
	}{
		{"關閉時句中選後方的字游標不動", false, false, 1, "郝", "他郝", 1, -1, 0},
		{"關閉時句首選字游標不動", false, false, 0, "她", "她好", 0, -1, 0},
		{"開啟時句中選後方的字移到詞尾（句尾）", true, false, 1, "郝", "他郝", 2, -1, 0},
		{"開啟時句首選字移到下一個字", true, false, 0, "她", "她好", 1, 1, 1},
		{"開啟時游標在句尾仍在句尾", true, false, 2, "郝", "他郝", 2, -1, 0},
		{"前方模式關閉時游標不動", false, true, 1, "她", "她好", 1, -1, 0},
		{"前方模式開啟時往後移一個字", true, true, 1, "她", "她好", 2, 1, 1},
		{"前方模式開啟時句尾不動", true, true, 2, "郝", "他郝", 2, -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := cursorSession()
			s.AdvanceAfterSelect = tc.advance
			s.CandidateBeforeCursor = tc.before
			typeSyl(s, "ㄊㄚ")
			typeSyl(s, "ㄏㄠˇ")
			key(s, proto.KeyHome)
			for range tc.at {
				key(s, proto.KeyRight)
			}
			snap := cursorChoose(t, s, key(s, proto.KeyDown), tc.chosen)
			if snap.Text() != tc.text || snap.Cursor.Rune != tc.cursor || snap.Candidates != nil || snap.Commit != "" {
				t.Fatalf("want %q cursor %d, got %+v", tc.text, tc.cursor, snap)
			}
			if tc.nextStart >= 0 {
				cursorRange(t, key(s, proto.KeyDown), tc.nextStart, tc.nextLength)
				key(s, proto.KeyEscape)
			}
			if got := key(s, proto.KeyEnter).Commit; got != tc.text {
				t.Fatalf("commit %q", got)
			}
		})
	}
}

// 多字詞：游標移到整個詞的後面；數字鍵與 Enter 選字走同一條規則，選到原本顯示的詞也算選定。
func TestAdvanceAfterSelectWholeWordAndEverySelectPath(t *testing.T) {
	for _, path := range []string{"select", "digit", "enter"} {
		for _, advance := range []bool{false, true} {
			s := cursorSession()
			s.AdvanceAfterSelect = advance
			typeSyl(s, "ㄈㄥ")
			typeSyl(s, "ㄐㄧㄥˇ")
			typeSyl(s, "ㄊㄚ")
			key(s, proto.KeyHome)
			snap := key(s, proto.KeyDown)
			cursorRange(t, snap, 0, 2)
			switch path {
			case "select":
				snap = cursorChoose(t, s, snap, "風井")
			case "digit":
				snap = key(s, "1")
			default:
				snap = key(s, proto.KeyEnter)
			}
			want := 0
			if advance {
				want = 2
			}
			if snap.Candidates != nil || snap.Cursor.Rune != want || snap.Commit != "" {
				t.Fatalf("%s advance=%v: want cursor %d, got %+v", path, advance, want, snap)
			}
			if advance {
				// 接著按 ↓ 直接改下一個字。
				cursorRange(t, key(s, proto.KeyDown), 2, 1)
			}
		}
	}
}
