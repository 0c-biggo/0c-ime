package session

import (
	"slices"
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #169：從輸入法選單切換「輸出簡體字」是全域設定，不是對這個輸入框的操作。快照照目前的畫面，
// 只跟著按鍵的暫存（Shift 誤觸還原、Tab 修正的撤銷、加詞標示……）都要留著。
var switchSimplified = proto.Request{Type: proto.OutputSimplified, On: true}

func TestOutputSimplifiedKeepsShiftRestore(t *testing.T) {
	s := newSession()
	pendingAfterWo(s)
	s.Handle(toEnglish)
	if snap := s.Handle(switchSimplified); !snap.Handled || snap.Text() != "我" || snap.Mode != proto.ModeEnglish {
		t.Fatalf("切換的快照照目前的畫面：%+v", snap)
	}
	if snap := s.Handle(restore); snap.Text() != "我ㄒㄧㄤ" || snap.Mode != proto.ModeZhuyin {
		t.Fatalf("切換之後再按 Shift 應放回清掉的注音：%+v", snap)
	}
}

func TestOutputSimplifiedKeepsPhoneticUndo(t *testing.T) {
	s := splitTypoSession()
	fzType(s, typoReading)
	fzSelect(t, s, 3, 1, "盡")
	requirePhoneticPreview(t, s.Handle(proto.Request{Type: proto.Cursor, To: 4}), "一年難盡", "一言難盡", 0)
	key(s, proto.KeyTab)
	s.Handle(switchSimplified)
	if got := key(s, proto.KeyBackspace).Text(); got != "一年難盡" {
		t.Fatalf("切換之後 Backspace 應撤銷 Tab 的修正：%q", got)
	}
}

func TestOutputSimplifiedKeepsMarkAndDoesNotCountAsAnAction(t *testing.T) {
	s := markSession()
	typeReadingLocal(s, "ㄨㄛˇ ㄏㄜˊ ㄕˋ ㄐㄧㄝˊ")
	selectAt(t, s, 2, "世")
	selectAt(t, s, 3, "傑")
	s.Handle(proto.Request{Type: proto.Cursor, To: 4})
	key(s, proto.KeyShiftLeft)
	before := key(s, proto.KeyShiftLeft)
	// 其他只跟著按鍵的狀態也不能動。
	s.paletteUndo = &literalSpan{0, 1}
	s.pendingToneKey = &toneKeyPending{at: 4, key: "3"}
	s.previewRewrite = true
	stash := &pendingStash{}
	s.toggleStash = stash

	snap := s.Handle(switchSimplified)
	if !hasMarked(snap) || !slices.Equal(snap.Notices, before.Notices) || snap.Revision != before.Revision || snap.Text() != before.Text() {
		t.Fatalf("切換的快照：%+v\n切換前：%+v", snap, before)
	}
	if s.mark == nil || s.paletteUndo == nil || s.pendingToneKey == nil || !s.previewRewrite || s.toggleStash != stash {
		t.Fatalf("暫存被清掉：mark %v palette %v tone %v preview %v stash %v", s.mark, s.paletteUndo, s.pendingToneKey, s.previewRewrite, s.toggleStash)
	}
	s.paletteUndo, s.pendingToneKey, s.previewRewrite, s.toggleStash = nil, nil, false, nil
	if snap := key(s, proto.KeyEnter); !slices.Contains(snap.Notices, "已加入詞庫：世傑") {
		t.Fatalf("切換之後標示還在、Enter 照樣加詞：%+v", snap)
	}
}
