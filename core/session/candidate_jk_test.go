package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #193：候選窗開著時，小寫 j 往前、k 往後換要修改的字（照新酷音），不必 Esc → ← → 再開候選。
// 新位置照 ↓ 開候選的規則：前方／後方模式、優先用現有分詞；已在頭尾時不動。

func jkSession(before bool) *Session {
	s := cursorSession()
	s.CandidateBeforeCursor = before
	for _, syl := range []string{"ㄊㄚ", "ㄏㄠˇ", "ㄨㄛˇ"} {
		typeSyl(s, syl)
	}
	return s
}

// jkWalk 依序按 keys，每一鍵後檢查候選範圍（起點、長度）且候選窗仍開著。
func jkWalk(t *testing.T, s *Session, keys string, want [][2]int) {
	t.Helper()
	for i, k := range keys {
		snap := key(s, string(k))
		if !snap.Handled || snap.Commit != "" {
			t.Fatalf("%d:%c handled=%v commit=%q", i, k, snap.Handled, snap.Commit)
		}
		cursorRange(t, snap, want[i][0], want[i][1])
		if !snap.Candidates.StepKeys {
			t.Fatalf("%d:%c: j/k still step here, the snapshot must say so: %+v", i, k, snap.Candidates)
		}
	}
}

func TestCandidateJKMovesSingleCharacterBothModes(t *testing.T) {
	// 後方模式（Linux／Windows 預設）：游標在 好 前面開候選，j 到 他、k 往後到 我；頭尾不動。
	s := jkSession(false)
	key(s, proto.KeyLeft)
	key(s, proto.KeyLeft)
	cursorRange(t, key(s, proto.KeyDown), 1, 1)
	jkWalk(t, s, "jjkkkj", [][2]int{{0, 1}, {0, 1}, {1, 1}, {2, 1}, {2, 1}, {1, 1}})
	// 換到的位置照常選字，游標停在照 ↓ 開窗的位置。
	snap := cursorChoose(t, s, s.snapshot(), "郝")
	if snap.Text() != "他郝我" || snap.Candidates != nil || snap.Cursor.Rune != 1 {
		t.Fatalf("%+v", snap)
	}

	// 前方模式（macOS 預設）：句尾開候選是 我，j 往前、k 往後，同樣頭尾不動。
	s = jkSession(true)
	cursorRange(t, key(s, proto.KeyDown), 2, 1)
	jkWalk(t, s, "kjjjkk", [][2]int{{2, 1}, {1, 1}, {0, 1}, {0, 1}, {1, 1}, {2, 1}})
	key(s, "j")
	key(s, "j")
	snap = cursorChoose(t, s, s.snapshot(), "她")
	if snap.Text() != "她好我" || snap.Cursor.Rune != 1 {
		t.Fatalf("%+v", snap)
	}
}

// 後方模式打完一句在句尾按空白，照規則改用前方模式選最後一個字；這時 j 要真的往前換到前一個字，
// 不能只把游標左移一格又開回同一個字。
func TestCandidateJKFromSentenceEndInAfterCursorMode(t *testing.T) {
	s := jkSession(false)
	cursorRange(t, key(s, " "), 2, 1)
	jkWalk(t, s, "kjjk", [][2]int{{2, 1}, {1, 1}, {0, 1}, {1, 1}})
}

// 有現成分詞時 j／k 一次換一個詞，換到的詞照設定的那一側開（#193 BD-0e2d73 回信）：前方模式游標在詞後、
// 選結束在游標的詞；後方模式游標在詞前、選從游標開始的詞。兩種模式走過的詞一樣，跟把游標放在詞的那一側再按 ↓ 相同。
// 以前一次只移一個字，前方模式從「風景」按 j 會停在「風」（詞的前半），後方模式從句尾的「好」按 j 會停在「景」（詞的後半），
// 從後面往前改時每個兩字詞都要多按一次，而且開出來的範圍跟 ↓ 的起點不同。
func TestCandidateJKStepsWholeWordsOnTheSettingSide(t *testing.T) {
	setup := func(before bool) *Session {
		s := cursorSession()
		for _, syl := range []string{"ㄨㄛˇ", "ㄈㄥ", "ㄐㄧㄥˇ", "ㄏㄠˇ"} {
			typeSyl(s, syl)
		}
		s.Handle(proto.Request{Type: proto.Cursor, To: 1})
		cursorChoose(t, s, s.Handle(proto.Request{Type: proto.Candidates, Length: 2}), "風景")
		s.CandidateBeforeCursor = before
		return s
	}
	// walk 每一鍵後另外檢查游標：前方模式在候選尾端，後方模式在候選起點。
	walk := func(t *testing.T, s *Session, keys string, want [][2]int) {
		t.Helper()
		jkWalk(t, s, keys, want)
		c := s.snapshot()
		at := c.Candidates.Start
		if s.CandidateBeforeCursor {
			at += c.Candidates.Length
		}
		if c.Cursor.Rune != at {
			t.Fatalf("before=%v cursor %d, want %d on the setting side of %+v", s.CandidateBeforeCursor, c.Cursor.Rune, at, c.Candidates)
		}
	}
	// 後方模式從頭往後：我 → 風景 → 好，尾端不動；再往前回到 風景 → 我。
	s := setup(false)
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	cursorRange(t, key(s, proto.KeyDown), 0, 1)
	walk(t, s, "kkkjj", [][2]int{{1, 2}, {3, 1}, {3, 1}, {1, 2}, {0, 1}})

	// 後方模式打完一句在句尾開候選（照規則改前方的詞）：j 換到前一個詞「風景」、游標放到詞前，不是「景」。
	s = setup(false)
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	cursorRange(t, key(s, proto.KeyDown), 3, 1)
	walk(t, s, "jjj", [][2]int{{1, 2}, {0, 1}, {0, 1}})

	// 前方模式從句尾往前：好 → 風景 → 我，頭不動；再往後回到 風景 → 好，尾端不動。
	s = setup(true)
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	cursorRange(t, key(s, proto.KeyDown), 3, 1)
	walk(t, s, "jjjkkk", [][2]int{{1, 2}, {0, 1}, {0, 1}, {1, 2}, {3, 1}, {3, 1}})
	key(s, "j")
	snap := cursorChoose(t, s, s.snapshot(), "風井")
	if snap.Text() != "我風井好" || snap.Cursor.Rune != 3 {
		t.Fatalf("%+v", snap)
	}

	// 游標在詞中間時 ↓ 照舊選半個詞；j／k 從那裡換到相鄰的整個詞。
	s = setup(true)
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	cursorRange(t, key(s, proto.KeyDown), 1, 1)
	walk(t, s, "kj", [][2]int{{1, 2}, {0, 1}})
	s = setup(false)
	s.Handle(proto.Request{Type: proto.Cursor, To: 2})
	cursorRange(t, key(s, proto.KeyDown), 2, 1)
	walk(t, s, "jk", [][2]int{{1, 2}, {3, 1}})
}

// 標點、英文等原樣文字整段算一格，換到它時跟 ↓ 一樣開符號候選。
func TestCandidateJKStepsOverLiterals(t *testing.T) {
	for _, before := range []bool{false, true} {
		for _, literal := range []string{"，", "👨‍👩‍👧‍👦"} {
			s := cursorSession()
			s.CandidateBeforeCursor = before
			typeSyl(s, "ㄊㄚ")
			s.insertLiteral(literal)
			typeSyl(s, "ㄏㄠˇ")
			n := len([]rune(literal))
			s.Handle(proto.Request{Type: proto.Cursor, To: map[bool]int{false: 0, true: 1}[before]})
			cursorRange(t, key(s, proto.KeyDown), 0, 1)
			jkWalk(t, s, "kkkjj", [][2]int{{1, n}, {1 + n, 1}, {1 + n, 1}, {1, n}, {0, 1}})
			if key(s, "k"); s.cand == nil || !s.cand.literal {
				t.Fatalf("before=%v %q: literal span opens symbol candidates: %+v", before, literal, s.cand)
			}
			key(s, "k")
			snap := cursorChoose(t, s, s.snapshot(), "郝")
			if snap.Text() != "他"+literal+"郝" {
				t.Fatalf("before=%v %q: %+v", before, literal, snap)
			}
		}
	}
}

// 選字鍵含 j、k（asdfghjkl;）時照舊選字；1234qweras 與預設的 1234567890 時 j、k 換字。
func TestCandidateJKSelectionKeysWin(t *testing.T) {
	s := manySession("asdfghjkl;")
	snap := openMany(t, s)
	if snap.Candidates.StepKeys {
		t.Fatalf("j/k are selection keys, the footer must not offer them: %+v", snap.Candidates)
	}
	want := snap.Candidates.Items[6].Text
	if snap = key(s, "j"); snap.Candidates != nil || snap.Text() != want {
		t.Fatalf("j 是第 7 個選字鍵：%+v", snap)
	}
	s = manySession("asdfghjkl;")
	snap = openMany(t, s)
	want = snap.Candidates.Items[7].Text
	if snap = key(s, "k"); snap.Candidates != nil || snap.Text() != want {
		t.Fatalf("k 是第 8 個選字鍵：%+v", snap)
	}
	for _, keys := range []string{"", "1234qweras"} {
		s := jkSession(false)
		s.SelectionKeys = keys
		key(s, proto.KeyLeft)
		cursorRange(t, key(s, proto.KeyDown), 2, 1)
		jkWalk(t, s, "jk", [][2]int{{1, 1}, {2, 1}})
		if s.snapshot().Text() != "他好我" {
			t.Fatalf("SelectionKeys=%q: j/k 不能選字：%q", keys, s.snapshot().Text())
		}
	}
	// 大寫（Shift＋J）不是換字鍵，跟以前一樣吃掉不動作。
	s = jkSession(false)
	key(s, proto.KeyLeft)
	key(s, proto.KeyDown)
	jkWalk(t, s, "JK", [][2]int{{2, 1}, {2, 1}})
}

// Emoji／顏文字選單、中英混打的按鍵候選、注音打到一半的符號候選、` 符號選單都不是「改某個字」，j/k 不動。
func TestCandidateJKIgnoredOutsideOrdinaryCandidates(t *testing.T) {
	unchanged := func(t *testing.T, name string, s *Session) {
		t.Helper()
		before := s.snapshot()
		if before.Candidates == nil || before.Candidates.StepKeys {
			t.Fatalf("%s: j/k do nothing here, the snapshot must not offer them: %+v", name, before.Candidates)
		}
		c := *s.cand
		for _, k := range []string{"j", "k"} {
			snap := key(s, k)
			if !snap.Handled || snap.Commit != "" || snap.Candidates == nil || snap.Text() != before.Text() ||
				snap.Cursor != before.Cursor || s.cand.start != c.start || s.cand.length != c.length ||
				s.cand.kind != c.kind || s.cand.mixed != c.mixed || s.cand.cursor != c.cursor {
				t.Fatalf("%s: %s changed the menu: %+v", name, k, snap)
			}
		}
	}
	for _, direct := range []bool{false, true} {
		s := paletteSession()
		if !direct {
			typeSyl(s, "ㄨㄛˇ")
			typeSyl(s, "ㄏㄠˇ")
		}
		key(s, "=")
		if s.cand == nil || s.cand.kind == "" || s.cand.direct != direct {
			t.Fatalf("palette fixture: %+v", s.cand)
		}
		unchanged(t, "emoji", s)
		key(s, proto.KeyTab)
		if s.cand == nil || s.cand.kind == "emoji" {
			t.Fatalf("palette tab: %+v", s.cand)
		}
		unchanged(t, s.cand.kind, s)
	}

	m := mixedSession()
	mixedType(m, "ji3hello")
	if key(m, proto.KeyDown); m.cand == nil || !m.cand.mixed {
		t.Fatalf("mixed fixture: %+v", m.cand)
	}
	unchanged(t, "mixed", m)

	p := jkSession(false)
	key(p, proto.KeyLeft)
	key(p, "j") // ㄨ，注音打到一半
	if key(p, proto.KeyDown); p.cand == nil || !p.cand.pending {
		t.Fatalf("pending fixture: %+v", p.cand)
	}
	unchanged(t, "pending", p)

	b := jkSession(false)
	key(b, proto.KeyLeft)
	if key(b, "`"); b.cand == nil || !b.cand.literal || b.cand.length != 0 {
		t.Fatalf("symbol menu fixture: %+v", b.cand)
	}
	unchanged(t, "symbols", b)
}

// 英文自動補字的候選也不是改字（#193 的頁尾提示要跟著核心）。
func TestCandidateJKNotOfferedForEnglishCompletion(t *testing.T) {
	s := autocompleteSession()
	mixedType(s, "hel")
	s.Handle(proto.Request{Type: proto.Close})
	snap := s.Handle(proto.Request{Type: proto.Candidates})
	if snap.Candidates == nil || !s.cand.english || snap.Candidates.StepKeys {
		t.Fatalf("english completion: %+v", snap.Candidates)
	}
}
