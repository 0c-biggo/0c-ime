package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 實際手順：注音打一半 → 誤按數字鍵盤 → 退格 → 補聲調。
// #90 起切到英文會清掉未打完的注音，暫存成字面注音只發生在注音模式插入英數時。
func TestResumeZhuyinAfterDeletingAccidentalDigits(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄔ")
	key(s, proto.KeyLeft)
	key(s, "j")
	key(s, "i")
	for _, k := range "1234" {
		key(s, "KP"+string(k))
	}
	for range 4 {
		key(s, proto.KeyBackspace)
	}
	if snap := key(s, "3"); snap.Text() != "我吃" || snap.Cursor.Rune != 1 || snap.Commit != "" {
		t.Fatalf("刪掉數字後應能接續原注音：%+v", snap)
	}
}

func TestEnglishCursorMovesThroughPendingLiterals(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄔ")
	key(s, proto.KeyLeft)
	key(s, "j")
	key(s, "i")
	key(s, "KP1") // ㄨㄛ 暫存成字面注音
	s.Handle(proto.Request{Type: proto.Toggle})
	key(s, proto.KeyLeft)
	if snap := key(s, proto.KeyLeft); snap.Text() != "ㄨㄛ1吃" || snap.Cursor.Rune != 1 {
		t.Fatalf("英文模式應可移入字面注音：%+v", snap)
	}
	if snap := key(s, "1"); snap.Text() != "ㄨ1ㄛ1吃" || snap.Cursor.Rune != 2 {
		t.Fatalf("英文插入位置應跟隨畫面游標：%+v", snap)
	}
	key(s, proto.KeyBackspace)
	key(s, proto.KeyRight)
	key(s, proto.KeyRight)
	key(s, proto.KeyBackspace)
	s.Handle(proto.Request{Type: proto.Toggle})
	// 已進入注音內部編輯，保留字面內容，不猜測使用者是否要重新組音。
	if snap := key(s, "3"); snap.Text() != "ㄨㄛ3吃" {
		t.Fatalf("編輯過的原樣注音不得還原舊狀態：%+v", snap)
	}
}

// #90：誤觸 Shift 立即切回注音，放回的是可接續的組音狀態，空白是一聲而不是開候選。
func TestFirstToneAfterRestoringPendingZhuyin(t *testing.T) {
	s := newSession()
	key(s, "t") // ㄔ
	s.Handle(proto.Request{Type: proto.Toggle})
	s.Handle(proto.Request{Type: proto.Toggle, Restore: true})
	if snap := key(s, " "); snap.Text() != "吃" || snap.Candidates != nil {
		t.Fatalf("回到未完成注音後空白應是一聲，不是開候選：%+v", snap)
	}
}

func TestPendingLiteralRestorationDoesNotUndoEdits(t *testing.T) {
	s := newSession()
	key(s, "j")
	key(s, "i")
	key(s, "KP1")
	key(s, proto.KeyLeft)
	key(s, proto.KeyLeft)
	key(s, proto.KeyBackspace) // 真正刪掉 ㄨ，不能之後又還原它。
	key(s, proto.KeyRight)
	key(s, proto.KeyRight)
	if snap := key(s, proto.KeyBackspace); snap.Text() != "ㄛ" {
		t.Fatalf("刪數字不能復活已刪注音：%+v", snap)
	}
	if snap := key(s, "3"); snap.Text() != "ㄛ3" {
		t.Fatalf("修改後的原樣注音應維持原樣：%+v", snap)
	}
}

func TestPendingLiteralKeepsOriginalBackspaceOrder(t *testing.T) {
	s := newSession()
	key(s, "i") // 先 ㄛ，再 ㄨ，退格應刪最後輸入的 ㄨ。
	key(s, "j")
	key(s, "A")
	key(s, proto.KeyBackspace)
	if snap := key(s, proto.KeyBackspace); snap.Text() != "ㄛ" {
		t.Fatalf("撤銷英數插入後應恢復組音退格順序：%+v", snap)
	}
}

func TestPendingLiteralResetDoesNotRestoreOldComposition(t *testing.T) {
	for _, end := range []string{proto.KeyEscape, proto.KeyEnter} {
		s := newSession()
		key(s, "j")
		key(s, "i")
		key(s, "A")
		key(s, end)
		typeSyl(s, "ㄔ")
		if snap := key(s, proto.KeyBackspace); snap.Text() != "" {
			t.Fatalf("%s 後不能還原上次組字：%+v", end, snap)
		}
	}
}

func TestEnglishInsertionPreservesSplitWord(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄇㄚˊ")
	if snap := typeSyl(s, "ㄈㄢˊ"); snap.Text() != "麻煩" {
		t.Fatalf("需先組出完整詞：%+v", snap)
	}
	key(s, proto.KeyLeft)
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, "1"); snap.Text() != "麻1煩" || snap.Cursor.Rune != 2 {
		t.Fatalf("中間插英數不能把既有漢字變回注音：%+v", snap)
	}
	if snap := key(s, proto.KeyBackspace); snap.Text() != "麻煩" || snap.Cursor.Rune != 1 {
		t.Fatalf("刪掉數字應還原原詞：%+v", snap)
	}
	key(s, "1")
	key(s, proto.KeyLeft)
	key(s, proto.KeyLeft)
	if snap := key(s, "A"); snap.Text() != "A麻1煩" || snap.Cursor.Rune != 1 {
		t.Fatalf("句首再插字時仍應保留漢字及游標位置：%+v", snap)
	}
	key(s, proto.KeyRight)
	if snap := key(s, proto.KeyBackspace); snap.Text() != "A1煩" {
		t.Fatalf("真正刪除漢字後不可留下舊字：%+v", snap)
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "A1煩" || snap.Text() != "" || s.model.Store.Total != 0 {
		t.Fatalf("提交保底畫面但不學入不完整讀音：%+v", snap)
	}
}

func TestEnglishInsertionDoesNotReselectSplitWord(t *testing.T) {
	s := newSession()
	for _, word := range []string{"煩", "凡"} {
		s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: word, Reading: zhuyin.ParseReading("ㄈㄢˊ"), PRW: 1})
	}
	s.model.LM.(*lm.Mem).SetUni("麻煩", 0.5)
	s.model.LM.(*lm.Mem).SetUni("煩", 0.0001)
	s.model.LM.(*lm.Mem).SetUni("凡", 0.1)
	key(s, "[") // 其他標點不能妨礙撤銷這次英數插入。
	typeSyl(s, "ㄇㄚˊ")
	if snap := typeSyl(s, "ㄈㄢˊ"); snap.Text() != "「麻煩" {
		t.Fatalf("需先組出原詞：%+v", snap)
	}
	key(s, proto.KeyLeft)
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, "1"); snap.Text() != "「麻1煩" {
		t.Fatalf("有其他同音字可解碼也不能擅自換字：%+v", snap)
	}
	key(s, "2")
	key(s, proto.KeyBackspace)
	if snap := key(s, proto.KeyBackspace); snap.Text() != "「麻煩" {
		t.Fatalf("撤銷數字應恢復原詞：%+v", snap)
	}
	if len(s.locks) != 0 {
		t.Fatal("刪掉分隔數字後應解除臨時文字保護")
	}
	key(s, "1")
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, proto.KeyEnter); snap.Commit != "「麻1煩" {
		t.Fatalf("提交仍須符合畫面：%+v", snap)
	}
	if len(s.model.Store.Picked) != 0 {
		t.Fatal("保護文字不能被學成手動選字")
	}
}

func TestMultiplePendingLiteralsRemainIndependent(t *testing.T) {
	s := newSession()
	key(s, "j")
	key(s, "i")
	key(s, "A")
	key(s, "v")
	key(s, "B")
	key(s, proto.KeyBackspace) // 還原 ㄒ
	key(s, proto.KeyBackspace) // 刪 ㄒ
	key(s, proto.KeyBackspace) // 刪 A、還原 ㄨㄛ
	if snap := key(s, "3"); snap.Text() != "我" {
		t.Fatalf("多段混合輸入逐段退格後仍能接續原注音：%+v", snap)
	}
}

func TestPendingLiteralFollowsEarlierEdits(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄔ")
	key(s, "j")
	key(s, "i")
	key(s, "A")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	key(s, "B")
	s.Handle(proto.Request{Type: proto.Cursor, To: 5})
	key(s, proto.KeyBackspace)
	if snap := key(s, "3"); snap.Text() != "B吃我" || snap.Cursor.Rune != 3 {
		t.Fatalf("前方插字不能讓暫存注音的位置失準：%+v", snap)
	}
}

func TestReplacingSymbolDoesNotConsumePendingZhuyin(t *testing.T) {
	s := newSession()
	key(s, "j")
	key(s, "]")
	key(s, proto.KeyDown)
	if snap := pickText(t, s, "』"); snap.Text() != "ㄨ』" {
		t.Fatalf("換符號不能順便還原或刪除前面的注音：%+v", snap)
	}
	key(s, proto.KeyBackspace)
	key(s, "i")
	if snap := key(s, "3"); snap.Text() != "我" {
		t.Fatalf("刪掉換過的符號仍能接續注音：%+v", snap)
	}
}
