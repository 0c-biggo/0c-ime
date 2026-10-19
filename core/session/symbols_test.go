package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/proto"
)

func pickText(t *testing.T, s *Session, text string) proto.Snapshot {
	t.Helper()
	if s.cand == nil {
		t.Fatal("候選沒有開啟")
	}
	for id, c := range s.cand.items {
		if c.Entry.Surface == text {
			return s.Handle(proto.Request{Type: proto.Select, Index: id})
		}
	}
	t.Fatalf("沒有候選 %q", text)
	return proto.Snapshot{}
}

func TestQuoteVariantsStayEditable(t *testing.T) {
	s := newSession()
	for _, c := range []struct{ key, want string }{{"[", "『"}, {"]", "』"}} {
		snap := key(s, c.key)
		if snap.Commit != "" {
			t.Fatal("符號鍵不應直接提交")
		}
		key(s, proto.KeyDown)
		snap = pickText(t, s, c.want)
		if snap.Commit != "" || snap.Candidates != nil {
			t.Fatalf("選符號應關候選而不提交：%+v", snap)
		}
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "『』" || snap.Text() != "" {
		t.Fatalf("Enter 應一次提交兩個符號：%+v", snap)
	}
	if s.model.Store.Total != 0 {
		t.Fatal("符號不能學成詞")
	}
}

// issue #46：中文狀態輸入 & 是全形的＆，與其他標點一樣留在組字區；要半形時按 ↓ 重選。
func TestAmpersandIsFullWidthInChineseMode(t *testing.T) {
	s := newSession()
	snap := key(s, "&")
	if !snap.Handled || snap.Text() != "＆" || snap.Commit != "" {
		t.Fatalf("空組字時 & 應成為組字區的 ＆：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "＆" {
		t.Fatalf("Enter 應提交全形 ＆：%+v", snap)
	}

	typeSyl(s, "ㄨㄛˇ")
	key(s, "&")
	typeSyl(s, "ㄔ")
	if snap = key(s, proto.KeyEnter); snap.Commit != "我＆吃" {
		t.Fatalf("中文之間的 & 應是全形：%+v", snap)
	}
	if s.model.Store.Uni["＆"] != 0 {
		t.Fatal("符號不能學成詞")
	}

	key(s, "&")
	key(s, proto.KeyDown)
	if snap = pickText(t, s, "&"); snap.Text() != "&" || snap.Commit != "" {
		t.Fatalf("在 ＆ 上按 ↓ 應能換回半形 &：%+v", snap)
	}
}

// issue #148：標準排列的 / 是 ㄥ，打不出斜線；Shift＋/ 的「？」按 ↓ 可換全形「／」與半形「/」，符號選單也有「／」。
func TestQuestionMarkGroupOffersSlash(t *testing.T) {
	for _, want := range []string{"／", "/"} {
		s := newSession()
		if snap := key(s, "?"); snap.Text() != "？" {
			t.Fatalf("Shift＋/ 應是全形問號：%+v", snap)
		}
		key(s, proto.KeyDown)
		if snap := pickText(t, s, want); snap.Text() != want || snap.Commit != "" {
			t.Fatalf("在 ？ 上按 ↓ 應能換成 %q：%+v", want, snap)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != want {
			t.Fatalf("Enter 應提交 %q：%+v", want, snap)
		}
	}
	palette := symbolPalette()
	found := false
	for _, item := range palette {
		found = found || item == "／"
	}
	if !found {
		t.Fatal("符號選單應列出全形斜線")
	}
}

func TestAmpersandStaysHalfWidthInEnglishMode(t *testing.T) {
	s := newSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, "&"); snap.Handled {
		t.Fatalf("英文模式沒有組字時 & 應交還應用程式：%+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Mode, On: false})
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	key(s, "&")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我&" {
		t.Fatalf("英文模式組字中的 & 維持半形：%+v", snap)
	}
}

func TestLiteralEditsAndLearningBoundaries(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "]")
	typeSyl(s, "ㄔ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	key(s, proto.KeyDown)
	if snap := pickText(t, s, "』"); snap.Text() != "我』吃" || snap.Cursor.Rune != 2 {
		t.Fatalf("中段替換符號：%+v", snap)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	if snap := key(s, "["); snap.Text() != "「我』吃" {
		t.Fatalf("符號插入後應保留右方內容：%+v", snap)
	}
	key(s, proto.KeyBackspace)
	if snap := key(s, proto.KeyEnter); snap.Commit != "我』吃" {
		t.Fatalf("刪除前方符號後的位置應正確：%+v", snap)
	}
	st := s.model.Store
	if st.Uni["我"] == 0 || st.Uni["吃"] == 0 || st.Uni["』"] != 0 || st.Bi["我\t吃"] != 0 || st.Bi[lm.BOS+"\t吃"] == 0 {
		t.Fatalf("兩側中文可學習，但不可跨越符號：%+v", st.Bi)
	}
}

func TestPendingZhuyinCanBeSelectedAsLiteral(t *testing.T) {
	s := newSession()
	key(s, ".") // ㄡ 尚未按聲調
	snap := key(s, proto.KeyDown)
	if !snap.Handled || snap.Candidates == nil || snap.Candidates.Items[0].Text != "ㄡ" {
		t.Fatalf("單獨注音應可選：%+v", snap)
	}
	key(s, proto.KeyEscape)
	if snap = key(s, proto.KeyBackspace); snap.Text() != "" {
		t.Fatal("取消候選後注音仍應可退格")
	}
	key(s, ".")
	key(s, proto.KeyDown)
	if snap = key(s, proto.KeyEnter); snap.Text() != "ㄡ" || snap.Commit != "" || !s.comp.Empty() {
		t.Fatalf("第一次 Enter 選取注音而不提交：%+v", snap)
	}
	if snap = typeSyl(s, "ㄨㄛˇ"); snap.Text() != "ㄡ我" {
		t.Fatalf("選取後可接著輸入中文：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "ㄡ我" || s.model.Store.Uni["ㄡ"] != 0 {
		t.Fatalf("注音不應被學成漢字讀音：%+v", snap)
	}
}

func TestConvertedSyllableCanReturnToLiteral(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyDown)
	snap := pickText(t, s, "ㄐㄧㄥˇ")
	if snap.Text() != "ㄐㄧㄥˇ" || snap.Cursor.Rune != 4 || snap.Cursor.UTF16 != 4 || snap.Commit != "" {
		t.Fatalf("多字元注音應逐字保留：%+v", snap)
	}
	if snap = key(s, proto.KeyBackspace); snap.Text() != "ㄐㄧㄥ" {
		t.Fatalf("原樣注音應可逐字刪除：%+v", snap)
	}
}

func TestSymbolPaletteAndCancel(t *testing.T) {
	s := newSession()
	snap := key(s, "`")
	if !snap.Handled || snap.Candidates == nil || snap.Candidates.Pages < 2 || snap.Commit != "" {
		t.Fatalf("反引號應開符號選單：%+v", snap)
	}
	key(s, proto.KeyPageDown)
	key(s, proto.KeyUp)
	if snap = pickText(t, s, "ㄡ"); snap.Text() != "ㄡ" || snap.Commit != "" {
		t.Fatalf("符號選單應包含注音：%+v", snap)
	}
	key(s, proto.KeyDown)
	key(s, proto.KeyUp) // 符號組內反向循環，不進入中文字詞候選
	key(s, proto.KeyEscape)
	if snap = key(s, proto.KeyEscape); snap.Text() != "" || snap.Commit != "" {
		t.Fatalf("Esc 應能放棄符號組字：%+v", snap)
	}
}

func TestPunctuationPreservesPendingAndPrefix(t *testing.T) {
	s := newSession()
	s.model.LM.(*lm.Mem).SetUni("麻煩", 0.01)
	before := typeSyl(s, "ㄇㄚˊ").Text()
	if snap := key(s, ">"); snap.Text() != before+"。" || snap.Commit != "" {
		t.Fatalf("加標點不能把前綴換字：%q → %+v", before, snap)
	}
	key(s, "v") // ㄒ 尚未完成
	if snap := key(s, "]"); snap.Text() != before+"。ㄒ」" {
		t.Fatalf("標點前的未完成注音不能消失或先送出：%+v", snap)
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != before+"。ㄒ」" {
		t.Fatalf("提交所見文字：%+v", snap)
	}
}

func TestSuggestionAndLocksAfterLiteral(t *testing.T) {
	s := newSession()
	s.model.LM.(*lm.Mem).SetUni("麻煩", 0.01)
	key(s, "[")
	snap := typeSyl(s, "ㄇㄚˊ")
	if snap.Suggestion != "煩" {
		t.Fatalf("符號後仍應補全：%+v", snap)
	}
	if snap = key(s, proto.KeyTab); snap.Text() != "「麻煩" {
		t.Fatalf("補全鎖定位置應含符號位移：%+v", snap)
	}
	key(s, "]")
	if snap = key(s, proto.KeyEnter); snap.Commit != "「麻煩」" {
		t.Fatalf("混合補全、標點提交：%+v", snap)
	}
}
