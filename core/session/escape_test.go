package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

// #76：未打完的注音先單獨清掉；再按一次 Esc 依設定清除整段，或保留已組好的字。
func TestEscapeClearsPendingZhuyinFirst(t *testing.T) {
	for _, keep := range []bool{false, true} {
		s := newSession()
		s.EscKeepComposition = keep
		typeSyl(s, "ㄨㄛˇ")
		typeSyl(s, "ㄒㄧㄤˇ")
		key(s, "t") // ㄔ 還沒打完
		if snap := key(s, proto.KeyEscape); !snap.Handled || snap.Text() != "我想" || snap.Commit != "" {
			t.Fatalf("keep=%v：第一次 Esc 只清未打完的注音：%+v", keep, snap)
		}
		snap := key(s, proto.KeyEscape)
		if !snap.Handled || snap.Commit != "" {
			t.Fatalf("keep=%v：組字中的 Esc 不交還應用程式、不提交：%+v", keep, snap)
		}
		if want := map[bool]string{false: "", true: "我想"}[keep]; snap.Text() != want {
			t.Fatalf("keep=%v：第二次 Esc 後 %q，要 %q", keep, snap.Text(), want)
		}
		if keep {
			key(s, proto.KeyBackspace)
			if snap := key(s, proto.KeyBackspace); snap.Text() != "" {
				t.Fatalf("保留的組字仍可用退格刪除：%q", snap.Text())
			}
		}
		if snap := key(s, proto.KeyEscape); snap.Handled {
			t.Fatalf("keep=%v：沒有組字時 Esc 交還應用程式", keep)
		}
	}
}

func TestEscapeOnlyPendingZhuyinEndsComposition(t *testing.T) {
	s := newSession()
	s.EscKeepComposition = true
	key(s, "t")
	if snap := key(s, proto.KeyEscape); !snap.Handled || snap.Text() != "" || s.Composing() {
		t.Fatalf("只有未打完的注音時，Esc 清掉後就沒有組字：%+v", snap)
	}
}

func TestEscapeClearsPendingMixedKeysFirst(t *testing.T) {
	s := newSession()
	s.MixedInput = true
	typeSyl(s, "ㄨㄛˇ")
	key(s, "j")
	key(s, "i")
	if snap := key(s, proto.KeyEscape); !snap.Handled || snap.Text() != "我" {
		t.Fatalf("中英混打未定的按鍵先清掉：%q", snap.Text())
	}
	if snap := key(s, proto.KeyEscape); snap.Text() != "" {
		t.Fatalf("預設第二次 Esc 清除整段：%q", snap.Text())
	}
}
