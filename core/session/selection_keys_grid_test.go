package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #112：多排時每排的個數跟著選字鍵走。1234qweras 每排 4 個，1234／qwer／as 對上鍵盤三排；
// 1234567890、asdfghjkl; 在鍵盤同一排，維持 5 個。直排與只有原生直排的外殼（Display 1）不受影響。
func TestSelectionKeysGridColumnsFollowKeyboardRows(t *testing.T) {
	for _, tc := range []struct {
		keys          string
		layout, want  int
		nativeDisplay bool
	}{
		{"", 5, 5, false}, {"1234567890", 5, 5, false}, {"asdfghjkl;", 5, 5, false}, {"1234qweras", 5, 4, false},
		{"1234qweras", 1, 0, false}, {"1234qweras", 5, 0, true},
	} {
		s := manySession(tc.keys)
		s.CandidateColumns = tc.layout
		if tc.nativeDisplay {
			s.Handle(proto.Request{Type: proto.Display, Columns: 1})
		}
		snap := openMany(t, s)
		if snap.Candidates.Columns != tc.want {
			t.Fatalf("SelectionKeys=%q layout=%d native=%v：每排 %d 個，要 %d", tc.keys, tc.layout, tc.nativeDisplay, snap.Candidates.Columns, tc.want)
		}
	}
}

// 一頁照舊 10 個，排成 4／4／2；方向鍵照格子位置移動，最後一排只有 a、s。
func TestSelectionKeysGridFourColumnsNavigation(t *testing.T) {
	s := manySession("1234qweras")
	s.CandidateColumns = 5
	snap := openMany(t, s)
	if len(snap.Candidates.Items) != 10 || itemKeys(snap) != "1234qweras" || snap.Candidates.Columns != 4 {
		t.Fatalf("一頁 10 個、每排 4 個、鍵標 1234qweras：%q %+v", itemKeys(snap), snap.Candidates)
	}
	for i, step := range []struct {
		key, label string
		want       int
	}{
		{proto.KeyRight, "2", 1}, {proto.KeyRight, "3", 2}, {proto.KeyRight, "4", 3},
		{proto.KeyRight, "4", 3}, // 第一排到底
		{proto.KeyDown, "r", 7},
		{proto.KeyDown, "s", 9}, // 最後一排沒有第 4 欄：停在最後一個
		{proto.KeyRight, "s", 9},
		{proto.KeyLeft, "a", 8}, {proto.KeyLeft, "a", 8},
		{proto.KeyUp, "q", 4}, {proto.KeyUp, "1", 0},
		{proto.KeyDown, "q", 4}, {proto.KeyDown, "a", 8},
		{proto.KeyDown, "1", 10}, // 下一頁同一欄
		{proto.KeyUp, "a", 8},    // 回上一頁最後一排同一欄
		{proto.KeyRight, "s", 9},
		{proto.KeyDown, "2", 11},
		{proto.KeyUp, "s", 9},
	} {
		snap = key(s, step.key)
		if s.cand.cursor != step.want {
			t.Fatalf("第 %d 步 %s：cursor=%d，要 %d", i+1, step.key, s.cand.cursor, step.want)
		}
		if got := snap.Candidates.Items[snap.Candidates.Highlight].Key; got != step.label {
			t.Fatalf("第 %d 步 %s：反白鍵標 %q，要 %q", i+1, step.key, got, step.label)
		}
	}
	// 第二頁第 4 欄往上：上一頁最後一排只有兩個，停在 s。
	s.cand.cursor = 13
	key(s, proto.KeyUp)
	if s.cand.cursor != 9 {
		t.Fatalf("第二頁第 4 欄往上：cursor=%d，要 9", s.cand.cursor)
	}
	// 翻頁照舊一頁 10 個。
	s.cand.cursor = 0
	if snap = key(s, proto.KeyPageDown); s.cand.cursor != 10 || snap.Candidates.Page != 1 || snap.Candidates.Columns != 4 {
		t.Fatalf("PgDn：cursor=%d %+v", s.cand.cursor, snap.Candidates)
	}
}

// Emoji 也跟著每排 4 個，鍵標同樣對上鍵盤三排；顏文字太寬，維持每排 2 個。
func TestSelectionKeysGridPaletteColumns(t *testing.T) {
	for _, tc := range []struct {
		keys           string
		emoji, kaomoji int
	}{{"1234567890", 5, 2}, {"asdfghjkl;", 5, 2}, {"1234qweras", 4, 2}} {
		for _, layout := range []int{1, 5} {
			s := paletteSession()
			s.CandidateColumns = layout
			s.SelectionKeys = tc.keys
			snap := key(s, "=")
			if snap.Candidates == nil || snap.Candidates.Kind != "emoji" || snap.Candidates.Columns != tc.emoji {
				t.Fatalf("%s layout=%d Emoji：%+v", tc.keys, layout, snap.Candidates)
			}
			snap = key(s, proto.KeyTab)
			if snap.Candidates == nil || snap.Candidates.Kind != "kaomoji" || snap.Candidates.Columns != tc.kaomoji {
				t.Fatalf("%s layout=%d 顏文字：%+v", tc.keys, layout, snap.Candidates)
			}
		}
	}
}
