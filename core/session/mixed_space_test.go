package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #78：中英混打時，注音打完後空白是半形空白，↓ 選字；注音打到一半時空白仍是一聲。
func TestMixedSpaceInsertsSpaceAfterCompletedSyllables(t *testing.T) {
	s := newSession()
	s.MixedInput = true
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, " "); !snap.Handled || snap.Candidates != nil || snap.Text() != "我 " {
		t.Fatalf("字尾空白插入半形空白：%+v", snap)
	}
	if snap := key(s, " "); snap.Text() != "我  " {
		t.Fatalf("連按兩次：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我  " {
		t.Fatalf("提交含空白：%q", snap.Commit)
	}
}

func TestMixedSpaceStillCompletesFirstTone(t *testing.T) {
	s := newSession()
	s.MixedInput = true
	key(s, "t")
	if snap := key(s, " "); snap.Text() != "吃" || snap.Candidates != nil {
		t.Fatalf("未打完的注音：空白完成一聲：%q", snap.Text())
	}
	if snap := key(s, " "); snap.Text() != "吃 " {
		t.Fatalf("完成後的空白插入空白：%q", snap.Text())
	}
}

func TestMixedSpaceInsertsAtCursorAndDownOpensCandidates(t *testing.T) {
	s := newSession()
	s.MixedInput = true
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄒㄧㄤˇ")
	key(s, proto.KeyLeft)
	if snap := key(s, " "); snap.Text() != "我 想" {
		t.Fatalf("游標在中間插入空白：%q", snap.Text())
	}
	if snap := key(s, proto.KeyDown); snap.Candidates == nil {
		t.Fatal("↓ 仍可開候選")
	}
}

func TestSpaceOpensCandidatesWithoutMixedInput(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, " "); snap.Candidates == nil || snap.Text() != "我" {
		t.Fatalf("純注音：空白照舊選字：%+v", snap)
	}
}

func TestMixedSpaceIdleGoesToApp(t *testing.T) {
	s := newSession()
	s.MixedInput = true
	if snap := key(s, " "); snap.Handled {
		t.Fatal("沒在組字時空白交還應用程式")
	}
}
