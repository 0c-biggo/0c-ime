package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #90：切到英文清掉未打完的注音；外殼確認中間沒有任何操作、立即切回注音時放回。

var (
	toEnglish = proto.Request{Type: proto.Toggle}
	restore   = proto.Request{Type: proto.Toggle, Restore: true}
)

// 我 + 尚未按聲調的 ㄒㄧㄤ
func pendingAfterWo(s *Session) {
	typeSyl(s, "ㄨㄛˇ")
	for _, k := range []string{"v", "u", ";"} {
		key(s, k)
	}
}

func TestToggleClearsPendingKeepsComposed(t *testing.T) {
	s := newSession()
	pendingAfterWo(s)
	if got := s.snapshot().Text(); got != "我ㄒㄧㄤ" {
		t.Fatalf("前置：%q", got)
	}
	snap := s.Handle(toEnglish)
	if !snap.Handled || snap.Text() != "我" || snap.Commit != "" || snap.Cursor.Rune != 1 || snap.Mode != proto.ModeEnglish || !s.comp.Empty() {
		t.Fatalf("清掉未打完的注音、保留已組好的字：%+v", snap)
	}
	if snap := key(s, "a"); snap.Text() != "我a" {
		t.Fatalf("接著打英文：%+v", snap)
	}
}

func TestImmediateToggleBackRestoresPending(t *testing.T) {
	s := newSession()
	pendingAfterWo(s)
	s.Handle(toEnglish)
	// 前文確認與欄數不是使用者操作，不影響還原。
	s.Handle(proto.Request{Type: proto.Context})
	s.Handle(proto.Request{Type: proto.Display, Columns: 1})
	snap := s.Handle(restore)
	if snap.Text() != "我ㄒㄧㄤ" || snap.Mode != proto.ModeZhuyin || snap.Cursor.Rune != 4 || snap.Commit != "" {
		t.Fatalf("立即切回注音應放回清掉的注音：%+v", snap)
	}
	if snap := key(s, "3"); snap.Text() != "我想" {
		t.Fatalf("放回的是可接續的組音狀態：%+v", snap)
	}
	// 再誤觸一次也一樣：每次切到英文都重新暫存。
	key(s, "v")
	s.Handle(toEnglish)
	if snap := s.Handle(restore); snap.Text() != "我想ㄒ" {
		t.Fatalf("第二次誤觸：%+v", snap)
	}
}

func TestToggleBackWithoutShellConfirmationDoesNotRestore(t *testing.T) {
	s := newSession()
	pendingAfterWo(s)
	s.Handle(toEnglish)
	// 舊外殼不送 Restore：無法確認切換之間沒有交給應用程式的按鍵，不還原。
	if snap := s.Handle(proto.Request{Type: proto.Toggle}); snap.Text() != "我" || !s.comp.Empty() {
		t.Fatalf("未確認的切回不還原：%+v", snap)
	}
}

func TestTypingInEnglishDropsRestore(t *testing.T) {
	for name, act := range map[string]func(*Session){
		"英文字母":   func(s *Session) { key(s, "a"); key(s, proto.KeyBackspace) },
		"游標移動":   func(s *Session) { key(s, proto.KeyLeft); key(s, proto.KeyRight) },
		"提交":     func(s *Session) { s.Handle(proto.Request{Type: proto.Commit}) },
		"再切一次模式": func(s *Session) { s.Handle(proto.Request{Type: proto.Mode, On: true}) },
	} {
		s := newSession()
		pendingAfterWo(s)
		s.Handle(toEnglish)
		act(s)
		if snap := s.Handle(restore); !s.comp.Empty() || len([]rune(snap.Text())) > 1 {
			t.Fatalf("%s：切到英文後有過操作，切回不還原：%+v", name, snap)
		}
	}
}

func TestOnlyPendingEndsComposition(t *testing.T) {
	s := newSession()
	for _, k := range []string{"v", "u", ";"} {
		key(s, k)
	}
	snap := s.Handle(toEnglish)
	if !snap.Handled || len(snap.Preedit) != 0 || snap.Commit != "" || s.Composing() {
		t.Fatalf("只有未打完的注音時，切到英文後就沒有組字：%+v", snap)
	}
	snap = s.Handle(restore)
	if snap.Text() != "ㄒㄧㄤ" || !s.Composing() || snap.Mode != proto.ModeZhuyin {
		t.Fatalf("立即切回重新開始組字：%+v", snap)
	}
	// 沒有組字時英文字母交還應用程式；核心仍收到按鍵，之後切回不還原。
	s.Handle(toEnglish)
	if snap := key(s, "a"); snap.Handled {
		t.Fatalf("沒有組字的英文字母交還應用程式：%+v", snap)
	}
	if snap := s.Handle(restore); len(snap.Preedit) != 0 || s.Composing() {
		t.Fatalf("打過英文後切回不還原：%+v", snap)
	}
}

func TestModeClearsPendingButDoesNotRestore(t *testing.T) {
	s := newSession()
	pendingAfterWo(s)
	if snap := s.Handle(proto.Request{Type: proto.Mode, On: true}); snap.Text() != "我" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("mode 切到英文同樣清掉未打完的注音：%+v", snap)
	}
	// mode 是外殼重連／啟用時的狀態還原，不是使用者的切換，不放回注音。
	if snap := s.Handle(proto.Request{Type: proto.Mode, On: false, Restore: true}); snap.Text() != "我" || !s.comp.Empty() {
		t.Fatalf("mode 切回注音不還原：%+v", snap)
	}
	// 已在英文模式時再設英文，沒有東西可清。
	s.Handle(toEnglish)
	if snap := s.Handle(proto.Request{Type: proto.Mode, On: true}); snap.Text() != "我" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("重複設定英文：%+v", snap)
	}
}

func TestToggleWithCandidatesOpen(t *testing.T) {
	// 已組好的字開著候選：照舊關閉候選、保留組字。
	s := newSession()
	typeSyl(s, "ㄐㄧㄥˇ")
	if snap := key(s, proto.KeyDown); snap.Candidates == nil {
		t.Fatalf("前置：候選應開啟：%+v", snap)
	}
	snap := s.Handle(toEnglish)
	if snap.Candidates != nil || snap.Text() != "景" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("候選開著時切英文：關閉候選、保留組字：%+v", snap)
	}
	if snap := s.Handle(restore); snap.Candidates != nil || snap.Text() != "景" {
		t.Fatalf("切回不重新開啟候選：%+v", snap)
	}

	// 未打完的注音開著候選（選取會用掉這些注音）：關閉候選，未打完的注音照常清掉，立即切回時放回（不重開候選）。
	s = newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "t") // ㄔ
	if snap := key(s, proto.KeyDown); snap.Candidates == nil || s.cand == nil || !s.cand.pending {
		t.Fatalf("前置：未打完注音的候選應開啟：%+v", snap)
	}
	snap = s.Handle(toEnglish)
	if snap.Candidates != nil || snap.Text() != "我" || !s.comp.Empty() {
		t.Fatalf("關閉候選並清掉未打完的注音：%+v", snap)
	}
	if snap := s.Handle(restore); snap.Candidates != nil || snap.Text() != "我ㄔ" {
		t.Fatalf("立即切回放回注音、不重開候選：%+v", snap)
	}
}

func TestMixedPendingKeysClearedAndRestored(t *testing.T) {
	s := mixedSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := mixedType(s, "xu"); snap.Text() != "我ㄌㄧ" {
		t.Fatalf("前置：暫存按鍵顯示成注音：%+v", snap)
	}
	if snap := s.Handle(toEnglish); snap.Text() != "我" || s.mixed.raw != "" {
		t.Fatalf("中英混打顯示成注音的暫存按鍵也要清掉：%+v", snap)
	}
	if snap := s.Handle(restore); snap.Text() != "我ㄌㄧ" || s.mixed.raw != "xu" {
		t.Fatalf("立即切回放回原始按鍵：%+v raw=%q", snap, s.mixed.raw)
	}
	if snap := key(s, proto.KeyEscape); snap.Text() != "我" || s.mixed.raw != "" {
		t.Fatalf("放回的是未定的暫存按鍵，Esc 第一下清掉：%+v", snap)
	}
}

func TestMixedEnglishPendingKeysStayLiteral(t *testing.T) {
	s := mixedSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := mixedType(s, "hel"); snap.Text() != "我hel" {
		t.Fatalf("前置：暫存按鍵顯示成英文：%+v", snap)
	}
	// 畫面上已是英文，不是未打完的注音：照舊留成字面文字，切到英文後接著打。
	if snap := s.Handle(toEnglish); snap.Text() != "我hel" {
		t.Fatalf("顯示成英文的暫存按鍵保留：%+v", snap)
	}
	if snap := mixedType(s, "lo"); snap.Text() != "我hello" {
		t.Fatalf("英文接著打：%+v", snap)
	}
}

func TestToggleClosesEmojiPalette(t *testing.T) {
	s := newSession()
	if snap := key(s, "="); snap.Candidates == nil {
		t.Fatalf("前置：表情選單應開啟：%+v", snap)
	}
	if snap := s.Handle(toEnglish); snap.Candidates != nil || s.Composing() {
		t.Fatalf("切換關閉表情選單：%+v", snap)
	}
	if snap := s.Handle(restore); snap.Candidates != nil || s.Composing() {
		t.Fatalf("切回不重開表情選單：%+v", snap)
	}
}
