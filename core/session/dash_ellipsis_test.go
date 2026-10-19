package session

import (
	"slices"
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #118：中文模式 Shift＋6（^）打刪節號「……」、Shift＋-（_）打破折號「——」，兩種排列都一樣；
// ↓ 可換成同組的其他寫法或半形原字元（同新酷音、小麥注音的 ^、_ 符號組）。破折號組補 ―（U+2015）、
// ─（U+2500）與 ← →，另有箭頭組。

func candidateTexts(s *Session) []string {
	if s.cand == nil {
		return nil
	}
	var out []string
	for _, c := range s.cand.items {
		out = append(out, c.Entry.Surface)
	}
	return out
}

func dashSessions(t *testing.T) map[string]*Session {
	return map[string]*Session{"標準": newSession(), "倚天": etenSession(t)}
}

func TestCaretAndUnderscoreTypeEllipsisAndDash(t *testing.T) {
	for _, c := range []struct{ key, want string }{{"^", "……"}, {"_", "——"}} {
		for name, s := range dashSessions(t) {
			snap := key(s, c.key)
			if !snap.Handled || snap.Text() != c.want || snap.Commit != "" || snap.Cursor.Rune != 2 {
				t.Fatalf("%s：沒有組字時 %q 應成為組字區的 %q：%+v", name, c.key, c.want, snap)
			}
			if snap = key(s, proto.KeyEnter); snap.Commit != c.want {
				t.Fatalf("%s：Enter 應提交 %q：%+v", name, c.want, snap)
			}
		}
	}
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "_")
	typeSyl(s, "ㄔ")
	key(s, "^")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我——吃……" {
		t.Fatalf("中文之間的破折號與刪節號：%+v", snap)
	}
	if s.model.Store.Uni["—"] != 0 || s.model.Store.Uni["…"] != 0 || s.model.Store.Bi["我\t吃"] != 0 {
		t.Fatal("符號不能學成詞，也不能讓兩側中文相連")
	}
	e := etenSession(t)
	typeETen(e, "ㄨㄛˇ")
	key(e, "_")
	typeETen(e, "ㄔ")
	if snap := key(e, proto.KeyEnter); snap.Commit != "我——吃" {
		t.Fatalf("倚天排列中文之間的破折號：%+v", snap)
	}
}

func TestEllipsisAndDashAlternatives(t *testing.T) {
	for _, c := range []struct {
		key  string
		want []string
	}{
		{"^", []string{"……", "⋯⋯", "…", "⋯", "︿", "^"}},
		{"_", []string{"——", "—", "―", "─", "－", "＿", "←", "→", "_"}},
	} {
		for name, s := range dashSessions(t) {
			key(s, c.key)
			key(s, proto.KeyDown)
			if got := candidateTexts(s); !slices.Equal(got, c.want) {
				t.Fatalf("%s：%q 上按 ↓ 應列出 %q，實際 %q", name, c.key, c.want, got)
			}
			// 整個「……」「——」是一個符號：選半形原字元會換掉兩個字，不留半個。
			if snap := pickText(t, s, c.key); snap.Text() != c.key || snap.Cursor.Rune != 1 || snap.Commit != "" {
				t.Fatalf("%s：應能換回半形 %q：%+v", name, c.key, snap)
			}
			key(s, proto.KeyDown)
			if got := candidateTexts(s); len(got) != len(c.want) || got[0] != c.key {
				t.Fatalf("%s：半形 %q 上按 ↓ 應回到同一組：%q", name, c.key, got)
			}
			if snap := pickText(t, s, c.want[0]); snap.Text() != c.want[0] {
				t.Fatalf("%s：應能再換回 %q：%+v", name, c.want[0], snap)
			}
			key(s, proto.KeyEscape)
		}
	}
	s := newSession()
	key(s, "_")
	key(s, proto.KeyDown)
	for _, dash := range []string{"―", "─", "—", "＿"} {
		if snap := pickText(t, s, dash); snap.Text() != dash {
			t.Fatalf("應能選 %q：%+v", dash, snap)
		}
		key(s, proto.KeyDown)
		if got := candidateTexts(s); got[0] != dash || !slices.Contains(got, "——") {
			t.Fatalf("%q 上按 ↓ 應列破折號組：%q", dash, got)
		}
	}
}

func TestEllipsisAndDashEditAsOneSymbol(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "^")
	if snap := key(s, proto.KeyLeft); snap.Cursor.Rune != 1 {
		t.Fatalf("左鍵應一次跨過整個刪節號：%+v", snap)
	}
	if snap := key(s, proto.KeyRight); snap.Cursor.Rune != 3 {
		t.Fatalf("右鍵應一次跨過整個刪節號：%+v", snap)
	}
	if snap := key(s, proto.KeyBackspace); snap.Text() != "我" {
		t.Fatalf("退格應刪掉整個刪節號：%+v", snap)
	}
	key(s, "_")
	key(s, "_")
	if snap := key(s, proto.KeyBackspace); snap.Text() != "我——" {
		t.Fatalf("連打兩次破折號，退格只刪後一個：%+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	if snap := key(s, proto.KeyDelete); snap.Text() != "我" {
		t.Fatalf("Del 應刪掉整個破折號：%+v", snap)
	}
	// 中段的破折號也能整個換掉。
	key(s, "_")
	typeSyl(s, "ㄔ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	key(s, proto.KeyDown)
	if snap := pickText(t, s, "―"); snap.Text() != "我―吃" || snap.Cursor.Rune != 2 {
		t.Fatalf("中段替換破折號：%+v", snap)
	}
}

func TestArrowsAndNewDashesInSymbols(t *testing.T) {
	s := newSession()
	key(s, "`")
	palette := candidateTexts(s)
	for _, want := range []string{"……", "⋯⋯", "——", "―", "─", "︿", "＿", "←", "→", "↑", "↓", "…", "⋯", "—", "－", "～", "＆", "ㄅ"} {
		if !slices.Contains(palette, want) {
			t.Errorf("符號選單應包含 %q", want)
		}
	}
	seen := map[string]bool{}
	for _, item := range palette {
		if seen[item] {
			t.Errorf("符號選單不應重複 %q", item)
		}
		seen[item] = true
	}
	key(s, proto.KeyEscape)

	// 破折號上按 ↓ 有 ← →（同新酷音的 _ 符號組）；箭頭上按 ↓ 換其他方向。
	for _, arrow := range []string{"←", "→"} {
		s := newSession()
		key(s, "_")
		key(s, proto.KeyDown)
		if snap := pickText(t, s, arrow); snap.Text() != arrow {
			t.Fatalf("破折號上按 ↓ 應可選 %q：%+v", arrow, snap)
		}
		key(s, proto.KeyDown)
		if got := candidateTexts(s); len(got) != 4 || got[0] != arrow || !slices.Equal(got[1:], arrowsWithout(arrow)) {
			t.Fatalf("%q 上按 ↓ 應換其他箭頭：%q", arrow, got)
		}
		if snap := pickText(t, s, "↑"); snap.Text() != "↑" {
			t.Fatalf("應可選 ↑：%+v", snap)
		}
	}
}

func arrowsWithout(arrow string) []string {
	var out []string
	for _, a := range []string{"←", "→", "↑", "↓"} {
		if a != arrow {
			out = append(out, a)
		}
	}
	return out
}

// 中英混打：識別字或英文後面的 _、^ 照舊是英文的一部分，不能變成破折號或刪節號。
func TestMixedKeepsUnderscoreAndCaretInEnglish(t *testing.T) {
	for _, raw := range []string{"user_name", "foo_bar123", "my_var", "snake_case_id", "x^2", "a_b"} {
		s := mixedSession()
		mixedType(s, raw)
		if snap := key(s, proto.KeyEnter); snap.Commit != raw {
			t.Errorf("%s -> %q", raw, snap.Commit)
		}
	}
	for _, c := range []struct{ keys, want string }{
		{"ji3_t ", "我——吃"},
		{"ji3^", "我……"},
		{"_", "——"},
	} {
		s := mixedSession()
		mixedType(s, c.keys)
		if snap := key(s, proto.KeyEnter); snap.Commit != c.want {
			t.Errorf("%q -> %q want %q", c.keys, snap.Commit, c.want)
		}
	}
}

// 英文模式維持半形。
func TestEnglishModeKeepsCaretAndUnderscore(t *testing.T) {
	s := newSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, "_"); snap.Handled {
		t.Fatalf("英文模式沒有組字時 _ 應交還應用程式：%+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	key(s, "_")
	key(s, "^")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我_^" {
		t.Fatalf("英文模式組字中的 _ ^ 維持半形：%+v", snap)
	}
}
