package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #75：數字鍵盤不是注音鍵。主鍵盤的 1 是 ㄅ，數字鍵盤的 1 一律是半形 1。
func TestKeypadIdleGoesBackToTheApp(t *testing.T) {
	s := newSession()
	for _, k := range []string{"KP1", "KP0", "KP8", "KP.", "KP+", "KP-", "KP*", "KP/"} {
		if snap := key(s, k); snap.Handled || snap.Text() != "" || s.Composing() {
			t.Fatalf("%s 沒在組字時交還應用程式：%+v", k, snap)
		}
	}
}

func TestKeypadInsertsHalfWidthWhileComposing(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "KP1")
	if snap := key(s, "KP."); !snap.Handled || snap.Text() != "我1." {
		t.Fatalf("組字中插在游標處：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEnter); snap.Commit != "我1." {
		t.Fatalf("提交：%q", snap.Commit)
	}
}

func TestKeypadKeepsPendingZhuyinAsSymbols(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "j") // ㄨ 還沒打完
	if snap := key(s, "KP2"); !snap.Handled || snap.Text() != "我ㄨ2" {
		t.Fatalf("未打完的注音保留成符號，數字接在後面：%q", snap.Text())
	}
}

func TestKeypadDigitsSelectCandidates(t *testing.T) {
	s := newSession()
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, proto.KeyDown)
	want := s.cand.items[1].Entry.Surface
	if snap := key(s, "KP2"); snap.Candidates != nil || snap.Text() != want {
		t.Fatalf("數字鍵盤 2 選第二個候選 %q：%+v", want, snap)
	}
	key(s, proto.KeyDown)
	if snap := key(s, "KP+"); !snap.Handled || snap.Candidates == nil || snap.Text() != want {
		t.Fatalf("候選開啟時運算鍵吃掉、不改組字：%+v", snap)
	}
}

func TestKeypadInEnglishModeIsPlainDigit(t *testing.T) {
	s := newSession()
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, "KP5"); snap.Handled {
		t.Fatal("英文模式沒在組字時交還應用程式")
	}
	s.Handle(proto.Request{Type: proto.Toggle})
	typeSyl(s, "ㄨㄛˇ")
	s.Handle(proto.Request{Type: proto.Toggle})
	if snap := key(s, "KP5"); !snap.Handled || snap.Text() != "我5" {
		t.Fatalf("英文模式組字中插入數字：%q", snap.Text())
	}
}

func TestKeypadInMixedInputDoesNotJoinPendingKeys(t *testing.T) {
	s := newSession()
	s.MixedInput = true
	typeSyl(s, "ㄨㄛˇ")
	if snap := key(s, "KP7"); !snap.Handled || snap.Text() != "我7" {
		t.Fatalf("中英混打時數字鍵盤不當輕聲：%q", snap.Text())
	}
}

func TestKeypadChar(t *testing.T) {
	for k, want := range map[string]rune{"KP1": '1', "KP.": '.', "KP/": '/', "KP=": '='} {
		if c, ok := proto.KeypadChar(k); !ok || c != want {
			t.Fatalf("%s → %q %v", k, c, ok)
		}
	}
	for _, k := range []string{"1", "KP", "KP12", "PageUp", "KP "} {
		if _, ok := proto.KeypadChar(k); ok {
			t.Fatalf("%q 不是數字鍵盤", k)
		}
	}
}
