package session

import (
	"slices"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// #161：候選窗開著時，目前詞長的候選後面接著列游標那個字的單字，翻頁就看得到，不必先按 Tab。

// shiwuSession 是回報裡的「如果事物」：ㄕˋ 有十個單字，詞與單字合起來超過一頁。
func shiwuSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		word, reading string
		p             float64
	}{
		{"如果", "ㄖㄨˊ ㄍㄨㄛˇ", .01}, {"如", "ㄖㄨˊ", .001}, {"果", "ㄍㄨㄛˇ", .001},
		{"事物", "ㄕˋ ㄨˋ", .002}, {"事務", "ㄕˋ ㄨˋ", .001}, {"飾物", "ㄕˋ ㄨˋ", .0001},
		{"是", "ㄕˋ", .01}, {"事", "ㄕˋ", .005}, {"試", "ㄕˋ", .004}, {"視", "ㄕˋ", .003},
		{"室", "ㄕˋ", .002}, {"式", "ㄕˋ", .002}, {"飾", "ㄕˋ", .001}, {"士", "ㄕˋ", .001},
		{"世", "ㄕˋ", .001}, {"市", "ㄕˋ", .001},
		{"物", "ㄨˋ", .003}, {"務", "ㄨˋ", .002}, {"誤", "ㄨˋ", .001},
	} {
		lex.Add(lexicon.Entry{Surface: e.word, Reading: zhuyin.ParseReading(e.reading), PRW: 1})
		sys.SetUni(e.word, e.p)
	}
	return New(personal.NewModel(lex, sys))
}

func typeRuguoShiwu(t *testing.T, s *Session) {
	t.Helper()
	for _, y := range []string{"ㄖㄨˊ", "ㄍㄨㄛˇ", "ㄕˋ", "ㄨˋ"} {
		typeSyl(s, y)
	}
	if got := s.snapshot().Text(); got != "如果事物" {
		t.Fatalf("fixture decodes %q", got)
	}
}

// allItems 收集整張清單（所有頁），之後回到第一頁第一項。
func allItems(s *Session) []proto.CandidateItem {
	var items []proto.CandidateItem
	for p := range s.pages() {
		items = append(items, s.Handle(proto.Request{Type: proto.Page, Index: p}).Candidates.Items...)
	}
	s.cand.cursor = 0
	for i := range items {
		items[i].ID, items[i].Key = 0, "" // 兩張清單的 ID 起點與頁內位置不同，比對內容就好
	}
	return items
}

// 後方模式單字取詞的第一個字，前方模式取最後一個字（都是緊貼游標的那個字）；單字那一段與按 Tab
// 換到單字時的清單完全相同：排序、顯示原因、原樣注音都一樣。
func TestMixedListAppendsSinglesOfTheCaretCharacter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool
		at     int
		single int
		char   string
	}{
		{"後方模式", false, 2, 2, "事"},
		{"前方模式", true, 4, 3, "物"},
		{"後方模式在句尾", false, 4, 3, "物"}, // 句尾改用前方的錨點（candidateBefore）
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := shiwuSession()
			s.CandidateBeforeCursor = tc.before
			typeRuguoShiwu(t, s)
			s.Handle(proto.Request{Type: proto.Cursor, To: tc.at})
			snap := key(s, proto.KeyDown)
			cursorRange(t, snap, 2, 2)
			if snap.Candidates.Title != "修改「事物」 · Tab 切換詞長" {
				t.Fatalf("title %q", snap.Candidates.Title)
			}
			split := s.cand.split
			mixed := allItems(s)
			if got := texts(mixed[:split]); !slices.Equal(got, []string{"事物", "事務", "飾物"}) {
				t.Fatalf("words first: %v", texts(mixed))
			}
			// 反白走進單字：範圍與標題都換成游標那個字。
			for range split {
				snap = key(s, proto.KeyDown)
			}
			cursorRange(t, snap, tc.single, 1)
			if want := "修改「" + tc.char + "」 · Tab 切換詞長"; snap.Candidates.Title != want {
				t.Fatalf("title %q, want %q", snap.Candidates.Title, want)
			}
			if snap.Text() != "如果事物" || snap.Commit != "" {
				t.Fatal("moving the highlight must not change the composition")
			}
			// Tab 照舊換到下一種詞長；這裡只有單字，清單與混合清單的單字段相同。
			snap = key(s, proto.KeyTab)
			cursorRange(t, snap, tc.single, 1)
			if s.cand.split != 0 {
				t.Fatal("a single-character list stays as it was")
			}
			singles := allItems(s)
			if !slices.Equal(mixed[split:], singles) {
				t.Fatalf("singles differ\nmixed %+v\nalone %+v", mixed[split:], singles)
			}
			if last := singles[len(singles)-1].Text; !strings.HasPrefix(last, "ㄕ") && !strings.HasPrefix(last, "ㄨ") {
				t.Fatalf("raw zhuyin must stay last: %q", last)
			}
			// 再按 Tab 回到兩個字的混合清單。
			cursorRange(t, key(s, proto.KeyTab), 2, 2)
			if s.cand.split != split {
				t.Fatal("Tab back must reopen the mixed list")
			}
		})
	}
}

// 選單字只換掉游標那個字，選詞照舊換整個詞；兩種都手選鎖定，數字鍵、Enter 與 Select 請求一致。
func TestMixedListSelection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		keys  []string
		text  string
		lock  int
		lockN int
	}{
		{"數字鍵選詞", []string{"2"}, "如果事務", 2, 2},
		{"翻到第二頁選單字", []string{proto.KeyPageDown, "1"}, "如果", 2, 1},
		{"方向鍵走到單字按 Enter", []string{proto.KeyDown, proto.KeyDown, proto.KeyDown, proto.KeyEnter}, "如果是", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := shiwuSession()
			typeRuguoShiwu(t, s)
			s.Handle(proto.Request{Type: proto.Cursor, To: 2})
			snap := key(s, proto.KeyDown)
			for _, k := range tc.keys {
				snap = key(s, k)
			}
			if snap.Candidates != nil || snap.Commit != "" || !strings.HasPrefix(snap.Text(), tc.text) || len([]rune(snap.Text())) != 4 {
				t.Fatalf("got %+v", snap)
			}
			if l, ok := s.locks[tc.lock]; !ok || l.Len != tc.lockN {
				t.Fatalf("locks %+v", s.locks)
			}
			if snap.Cursor.Rune != 2 {
				t.Fatalf("cursor moved without advance-after-select: %d", snap.Cursor.Rune)
			}
		})
	}
}

// 選單字之後學到的是單字（下一次開窗排第一），不是被換掉的詞。
func TestMixedListSingleSelectionLearnsTheCharacter(t *testing.T) {
	s := shiwuSession()
	typeRuguoShiwu(t, s)
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	snap := cursorChoose(t, s, key(s, proto.KeyDown), "試")
	if !strings.HasPrefix(snap.Text(), "如果試") {
		t.Fatalf("%q", snap.Text())
	}
	key(s, proto.KeyEnter)
	if !s.model.PickedSingle(zhuyin.Syllable("ㄕˋ"), "試") {
		t.Fatal("single pick not learned")
	}
}

// 「選字後游標移到下一個字」（#133）：選單字時移到那個字後面；前方模式往後移一個字，句尾不動。
func TestMixedListAdvanceAfterSingle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     bool
		at, cursor int
	}{
		{"後方模式", false, 2, 3},
		{"前方模式", true, 3, 4},
		{"前方模式句尾", true, 4, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := shiwuSession()
			s.AdvanceAfterSelect, s.CandidateBeforeCursor = true, tc.before
			typeRuguoShiwu(t, s)
			s.Handle(proto.Request{Type: proto.Cursor, To: tc.at})
			key(s, proto.KeyDown)
			s.cand.cursor = s.cand.split + 1 // 第二個單字
			snap := key(s, proto.KeyEnter)
			if snap.Candidates != nil || snap.Cursor.Rune != tc.cursor {
				t.Fatalf("cursor %d, want %d: %+v", snap.Cursor.Rune, tc.cursor, snap)
			}
		})
	}
}

// Home／End（#106）：End 到整張清單最後一項（單字的原樣注音），選它只把游標那個字換成注音。
func TestMixedListEndAndRawZhuyin(t *testing.T) {
	s := shiwuSession()
	typeRuguoShiwu(t, s)
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	key(s, proto.KeyDown)
	snap := key(s, proto.KeyEnd)
	cursorRange(t, snap, 2, 1)
	if c := snap.Candidates; c.Page != c.Pages-1 || c.Items[c.Highlight].Text != "ㄕˋ" {
		t.Fatalf("end: %+v", c)
	}
	if snap = key(s, proto.KeyHome); snap.Candidates.Page != 0 || snap.Candidates.Highlight != 0 {
		t.Fatalf("home: %+v", snap.Candidates)
	}
	key(s, proto.KeyEnd)
	snap = key(s, proto.KeyEnter)
	if got := snap.Text(); !strings.HasPrefix(got, "如果ㄕˋ") || len([]rune(got)) != 5 {
		t.Fatalf("raw zhuyin must replace only the caret character: %q", got)
	}
}

// 反悔鍵（#123）作用在反白的那一項：反白單字時說的是那個字。
func TestMixedListUndoKeyActsOnHighlightedSingle(t *testing.T) {
	s := shiwuSession()
	s.Preview02 = true
	typeRuguoShiwu(t, s)
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	key(s, proto.KeyDown)
	s.cand.cursor = s.cand.split // 第一個單字
	word := s.cand.items[s.cand.cursor].Entry.Surface
	snap := key(s, proto.KeyShiftDelete)
	if len(snap.Notices) == 0 || !strings.Contains(snap.Notices[0], "「"+word+"」") {
		t.Fatalf("notice %v", snap.Notices)
	}
	if snap.Candidates == nil || s.cand.split == 0 {
		t.Fatal("the mixed list stays open")
	}
}

// 已經是單字詞長時維持原樣：游標在單字分詞上開窗，清單只有單字。
func TestMixedListSingleLengthUnchanged(t *testing.T) {
	s := cursorSession()
	typeSyl(s, "ㄊㄚ")
	typeSyl(s, "ㄏㄠˇ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := key(s, proto.KeyDown)
	cursorRange(t, snap, 0, 1)
	if s.cand.split != 0 || !slices.Equal(texts(snap.Candidates.Items), []string{"他", "她", "它", "ㄊㄚ"}) {
		t.Fatalf("%v", texts(snap.Candidates.Items))
	}
}

// 模糊音（#48）：兩段的近音附註各自對齊；目前詞長只有近音時，原音的單字接在後面。
func TestMixedListKeepsNearNotesInBothParts(t *testing.T) {
	s := fuzzySession(allPairs(), fillers("ㄘ ㄈㄢˋ")...)
	fzType(s, "ㄘ ㄈㄢˋ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 2})
	cursorRange(t, snap, 0, 2)
	split := s.cand.split
	mixed := allItems(s)
	if it, ok := find(mixed[:split], "吃飯"); !ok || it.Note != "近音 ㄔ ㄈㄢˋ" {
		t.Fatalf("word note: %+v", mixed[:split])
	}
	s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if alone := allItems(s); !slices.Equal(mixed[split:], alone) {
		t.Fatalf("singles differ\nmixed %+v\nalone %+v", mixed[split:], alone)
	}
	near := false
	for _, it := range mixed[split:] {
		near = near || strings.HasPrefix(it.Note, "近音")
	}
	if !near {
		t.Fatalf("near singles keep their notes: %+v", mixed[split:])
	}
}
