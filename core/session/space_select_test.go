package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #107、#110：關閉「空白鍵選字」時，一般注音模式的注音打完後空白插入半形空白，↓ 選字。
func TestSpaceInsertsSpaceWhenSelectionOff(t *testing.T) {
	s := newSession()
	s.SpaceInsertsSpace = true
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, " "); !snap.Handled || snap.Candidates != nil || snap.Text() != "我 " || snap.Commit != "" {
		t.Fatalf("注音打完後空白插入半形空白、不開候選：%+v", snap)
	}
	typeSyl(s, "ㄒㄧㄤˇ")
	if snap := key(s, " "); snap.Text() != "我 想 " || snap.Candidates != nil {
		t.Fatalf("繼續打字後再按空白：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我 想 " {
		t.Fatalf("Enter 提交含空白：%q", snap.Commit)
	}
}

func TestSpaceStillCompletesFirstToneWhenSelectionOff(t *testing.T) {
	s := newSession()
	s.SpaceInsertsSpace = true
	key(s, "t") // ㄔ 還沒打完
	if snap := key(s, " "); !snap.Handled || snap.Text() != "吃" || snap.Candidates != nil {
		t.Fatalf("未打完的注音：空白仍是一聲：%+v", snap)
	}
	if snap := key(s, " "); snap.Text() != "吃 " {
		t.Fatalf("一聲完成後的空白插入空白：%q", snap.Text())
	}
}

func TestDownSelectsAndSpacePagesWhenSelectionOff(t *testing.T) {
	s := newSession()
	s.SpaceInsertsSpace = true
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄒㄧㄤˇ")
	key(s, proto.KeyLeft)
	if snap := key(s, " "); snap.Text() != "我 想" || snap.Candidates != nil {
		t.Fatalf("游標在中間插入空白：%q", snap.Text())
	}
	open := key(s, proto.KeyDown)
	if open.Candidates == nil {
		t.Fatal("↓ 仍可開候選")
	}
	// 候選開著時空白照舊換頁（只有一頁時繞回第一頁），不插入空白也不關候選。
	if snap := key(s, " "); snap.Candidates == nil || snap.Text() != open.Text() {
		t.Fatalf("候選開著時空白換頁：%+v", snap)
	}
}

func TestSpaceIdleGoesToAppWhenSelectionOff(t *testing.T) {
	s := newSession()
	s.SpaceInsertsSpace = true
	if snap := key(s, " "); snap.Handled {
		t.Fatal("沒在組字時空白交還應用程式")
	}
}

// 預設（零值）維持空白選字；中英混打不論開關都插入空白。
func TestSpaceSelectionDefaultAndMixedInput(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, " "); snap.Candidates == nil || snap.Text() != "我" {
		t.Fatalf("預設空白開候選：%+v", snap)
	}
	for _, off := range []bool{false, true} {
		s := newSession()
		s.MixedInput = true
		s.SpaceInsertsSpace = off
		typeSyl(s, "ㄨㄛˇ")
		if snap := key(s, " "); snap.Candidates != nil || snap.Text() != "我 " {
			t.Fatalf("off=%v：中英混打照舊插入空白：%+v", off, snap)
		}
	}
}
