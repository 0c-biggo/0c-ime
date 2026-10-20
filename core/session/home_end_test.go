package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #106：組字中 Home／End 移到組字區頭尾，不交給應用程式；沒在組字時照常交還。
func TestHomeEndMoveWithinComposition(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄒㄧㄤˇ")
	typeSyl(s, "ㄔ")
	snap := key(s, proto.KeyHome)
	if !snap.Handled || snap.Commit != "" || snap.Text() != "我想吃" || snap.Cursor.Rune != 0 {
		t.Fatalf("Home 應移到組字區最前：%+v", snap)
	}
	if snap = key(s, proto.KeyHome); !snap.Handled || snap.Cursor.Rune != 0 {
		t.Fatalf("已在最前仍應吃掉 Home：%+v", snap)
	}
	snap = key(s, proto.KeyEnd)
	if !snap.Handled || snap.Text() != "我想吃" || snap.Cursor.Rune != 3 {
		t.Fatalf("End 應移到組字區最後：%+v", snap)
	}
	key(s, proto.KeyHome)
	if snap = typeSyl(s, "ㄔ"); snap.Text() != "吃我想吃" || snap.Cursor.Rune != 1 {
		t.Fatalf("Home 之後應從最前插入：%+v", snap)
	}
	for _, k := range []string{proto.KeyHome, proto.KeyEnd} {
		s := newSession()
		if snap := key(s, k); snap.Handled || snap.Commit != "" {
			t.Fatalf("閒置 %s 應交還應用程式：%+v", k, snap)
		}
	}
}

// 音節打到一半時同左右鍵：游標不動、按鍵照樣吃掉，組字區不被應用程式的游標拋下。
func TestHomeEndWithPendingSyllableLikeArrows(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄔ")
	before := key(s, "v") // ㄒ
	for _, k := range []string{proto.KeyHome, proto.KeyEnd, proto.KeyLeft} {
		snap := key(s, k)
		if !snap.Handled || snap.Text() != before.Text() || snap.Cursor != before.Cursor {
			t.Fatalf("%s：未打完的注音應留在原位：%+v，原本 %+v", k, snap, before)
		}
	}
	key(s, "u")
	key(s, ";")
	if snap := key(s, "3"); snap.Text() != "我吃想" { // ㄧㄤˇ 補完仍在原位
		t.Fatalf("補完音節：%+v", snap)
	}
	if snap := key(s, proto.KeyHome); snap.Cursor.Rune != 0 {
		t.Fatalf("音節完成後 Home 應移動：%+v", snap)
	}
}

// 候選開啟時 Home／End 到第一個／最後一個候選（比照小麥、威注音）；Backspace／Del 同 Esc 關閉候選、保留組字。
func TestCandidateHomeEndBackspaceDelete(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	snap := key(s, "`")
	if snap.Candidates == nil || snap.Candidates.Pages < 3 {
		t.Fatalf("符號選單應有多頁：%+v", snap.Candidates)
	}
	key(s, proto.KeyPageDown)
	snap = key(s, proto.KeyEnd)
	c := snap.Candidates
	if !snap.Handled || c == nil || c.Page != c.Pages-1 || c.Highlight != len(c.Items)-1 {
		t.Fatalf("End 應到最後一個候選：%+v", c)
	}
	snap = key(s, proto.KeyHome)
	if !snap.Handled || snap.Candidates == nil || snap.Candidates.Page != 0 || snap.Candidates.Highlight != 0 {
		t.Fatalf("Home 應到第一個候選：%+v", snap.Candidates)
	}
	if snap.Text() != "我" || snap.Cursor.Rune != 1 {
		t.Fatalf("候選中的 Home 不移動組字游標：%+v", snap)
	}
	for _, k := range []string{proto.KeyBackspace, proto.KeyDelete, proto.KeyBackTab} {
		s := newSession()
		typeSyl(s, "ㄨㄛˇ")
		typeSyl(s, "ㄔ")
		key(s, proto.KeyLeft)
		if snap := key(s, proto.KeyDown); snap.Candidates == nil {
			t.Fatalf("%s：應開候選：%+v", k, snap)
		}
		snap := key(s, k)
		closed := k != proto.KeyBackTab
		if !snap.Handled || snap.Commit != "" || snap.Text() != "我吃" || snap.Cursor.Rune != 1 || (snap.Candidates == nil) != closed {
			t.Fatalf("候選中 %s：%+v", k, snap)
		}
	}
}

// 組字中候選沒開時 ↑、PgUp、PgDn、Shift+Tab、沒有補全的 Tab 吃掉不動作；沒有組字時交還應用程式（#106）。
func TestComposingNavigationKeysStayInComposition(t *testing.T) {
	keys := []string{proto.KeyUp, proto.KeyPageUp, proto.KeyPageDown, proto.KeyBackTab, proto.KeyTab}
	for _, english := range []bool{false, true} {
		for _, k := range keys {
			s := newSession()
			typeSyl(s, "ㄨㄛˇ")
			typeSyl(s, "ㄔ")
			key(s, proto.KeyLeft)
			if english {
				s.Handle(proto.Request{Type: proto.Toggle})
			}
			before := key(s, proto.KeyRight)
			key(s, proto.KeyLeft)
			snap := key(s, k)
			if !snap.Handled || snap.Commit != "" || snap.Text() != before.Text() || snap.Cursor.Rune != 1 || snap.Candidates != nil {
				t.Fatalf("english=%v 組字中 %s 應吃掉不動作：%+v", english, k, snap)
			}
		}
		for _, k := range keys {
			s := newSession()
			s.Handle(proto.Request{Type: proto.Mode, On: english})
			if snap := key(s, k); snap.Handled || snap.Commit != "" {
				t.Fatalf("english=%v 沒有組字時 %s 應交還應用程式：%+v", english, k, snap)
			}
		}
	}
	// 未打完的注音也算組字。
	s := newSession()
	key(s, "j")
	for _, k := range keys {
		if snap := key(s, k); !snap.Handled || snap.Text() != "ㄨ" {
			t.Fatalf("未打完的注音 %s：%+v", k, snap)
		}
	}
	// 英文模式保留組字時 ↓ 交給應用程式的既有行為不變。
	s = newSession()
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, proto.KeyDown); snap.Handled {
		t.Fatalf("英文模式 ↓ 照舊交給應用程式：%+v", snap)
	}
}

// 英文模式下保留的中文組字，以及中英混打尚未定案的英文，Home／End 一樣在組字區內移動。
func TestHomeEndInEnglishAndMixedComposition(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(proto.Request{Type: proto.Toggle})
	key(s, "o")
	key(s, "k")
	if snap := key(s, proto.KeyHome); !snap.Handled || snap.Text() != "我ok" || snap.Cursor.Rune != 0 {
		t.Fatalf("英文模式 Home：%+v", snap)
	}
	if snap := key(s, proto.KeyEnd); !snap.Handled || snap.Cursor.Rune != 3 {
		t.Fatalf("英文模式 End：%+v", snap)
	}

	s = mixedSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "o")
	key(s, "k")
	snap := key(s, proto.KeyHome)
	if !snap.Handled || snap.Cursor.Rune != 0 || snap.Commit != "" {
		t.Fatalf("中英混打 Home：%+v", snap)
	}
	if snap = key(s, proto.KeyEnd); !snap.Handled || snap.Cursor.Rune != len([]rune(snap.Text())) {
		t.Fatalf("中英混打 End：%+v", snap)
	}
}
