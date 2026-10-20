package engine

import (
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

func sharedDo(t *testing.T, e *Engine) func(uint64, proto.Request) proto.Snapshot {
	return func(id uint64, r proto.Request) proto.Snapshot {
		t.Helper()
		snap, err := e.Do(id, r)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
}

// #173 code review：全部共用（#115）時，一個輸入框開著 Caps Lock 的暫時英文，別的輸入框之後切換（最後回到注音）：
// 這個輸入框的下一個字母不能先「跟上別的輸入框」而變成注音；關掉 Caps Lock 才回到共用的模式。
func TestSharedModeKeepsCapsLockEnglishUntilReleased(t *testing.T) {
	e := testEngine()
	e.SharedMode, e.CapsLockEnglish = true, true
	a, b := e.OpenSession(), e.OpenSession()
	do := sharedDo(t, e)
	if snap := do(a, proto.Request{Type: proto.CapsLock, On: true}); snap.Mode != proto.ModeEnglish || !snap.CapsEnglish {
		t.Fatalf("開 Caps Lock 切到暫時英文：%+v", snap)
	}
	do(b, proto.Request{Type: proto.Toggle})
	if snap := do(b, proto.Request{Type: proto.Toggle}); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("另一個輸入框切兩次回到注音：%+v", snap)
	}
	for _, k := range []string{"A", "B"} {
		if snap := do(a, proto.Request{Type: proto.Key, Key: k}); snap.Mode != proto.ModeEnglish || snap.Handled || !snap.CapsLock || !snap.CapsEnglish {
			t.Fatalf("Caps Lock 還開著，%s 應照英文交給應用程式：%+v", k, snap)
		}
	}
	if snap := do(a, proto.Request{Type: proto.CapsLock, On: false}); snap.Mode != proto.ModeZhuyin || snap.CapsLock || snap.CapsEnglish {
		t.Fatalf("關 Caps Lock 回到共用的注音：%+v", snap)
	}
	if snap := do(a, proto.Request{Type: proto.Key, Key: "j"}); snap.Mode != proto.ModeZhuyin || snap.Text() != "ㄨ" {
		t.Fatalf("回到注音後照打注音：%+v", snap)
	}

	// Shift 切的英文再開 Caps Lock（只記下狀態）：別的輸入框切回注音時，Caps Lock 還開著就維持英文；關掉才回到共用的模式。
	do(a, proto.Request{Type: proto.Key, Key: proto.KeyEscape})
	do(a, proto.Request{Type: proto.Toggle}) // 共用的模式變成英文
	if snap := do(a, proto.Request{Type: proto.CapsLock, On: true}); snap.Mode != proto.ModeEnglish || snap.CapsEnglish || !snap.CapsLock {
		t.Fatalf("已經是英文時開 Caps Lock 只記下狀態：%+v", snap)
	}
	do(b, proto.Request{Type: proto.Toggle}) // 共用的模式回到注音
	if snap := do(a, proto.Request{Type: proto.Key, Key: "A"}); snap.Mode != proto.ModeEnglish || snap.Handled || !snap.CapsLock {
		t.Fatalf("Caps Lock 還開著，A 應照英文交給應用程式：%+v", snap)
	}
	if snap := do(a, proto.Request{Type: proto.CapsLock, On: false}); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("關 Caps Lock 回到共用的注音：%+v", snap)
	}

	// Caps Lock 開著、自己切回注音的輸入框：別的輸入框切到英文時照常跟上（跟 Caps Lock 一樣是英文）。
	do(a, proto.Request{Type: proto.CapsLock, On: true})
	if snap := do(a, proto.Request{Type: proto.Toggle}); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("Caps Lock 開著時 Shift 照常切回注音：%+v", snap)
	}
	do(b, proto.Request{Type: proto.Toggle}) // 共用的模式變成英文
	if snap := do(a, proto.Request{Type: proto.Key, Key: "A"}); snap.Mode != proto.ModeEnglish {
		t.Fatalf("別的輸入框切到英文：跟上共用的英文：%+v", snap)
	}
}

// macOS 的 Caps Lock（#84）送「設成英文」，全部共用時只影響這個輸入框：別的輸入框之後切換也不能把它拉回注音，
// 直到 Caps Lock 關掉（「設成中文」回到共用的模式）或在這個輸入框切換。
func TestSharedModeKeepsModeOnEnglishUntilReleased(t *testing.T) {
	e := testEngine()
	e.SharedMode = true
	a, b := e.OpenSession(), e.OpenSession()
	do := sharedDo(t, e)
	if snap := do(a, proto.Request{Type: proto.Mode, On: true}); snap.Mode != proto.ModeEnglish {
		t.Fatalf("設成英文：%+v", snap)
	}
	do(b, proto.Request{Type: proto.Toggle})
	do(b, proto.Request{Type: proto.Toggle})
	if snap := do(a, proto.Request{Type: proto.Key, Key: "A"}); snap.Mode != proto.ModeEnglish || snap.Handled {
		t.Fatalf("Caps Lock 還開著，A 應照英文交給應用程式：%+v", snap)
	}
	if snap := do(a, proto.Request{Type: proto.Mode, On: false}); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("關掉 Caps Lock 回到共用的注音：%+v", snap)
	}
	// 回到共用之後照常跟上別的輸入框。
	do(b, proto.Request{Type: proto.Toggle})
	if snap := do(a, proto.Request{Type: proto.Key, Key: "a"}); snap.Mode != proto.ModeEnglish {
		t.Fatalf("回到共用後跟上別的輸入框：%+v", snap)
	}
	// 共用的模式已經是英文時的「設成英文」不是暫時的：別的輸入框切回注音時照常跟上（Windows 重開 session 還原共用的英文）。
	c := e.OpenSession()
	do(c, proto.Request{Type: proto.Mode, On: true})
	do(b, proto.Request{Type: proto.Toggle})
	if snap := do(c, proto.Request{Type: proto.Key, Key: "j"}); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("跟上共用的注音：%+v", snap)
	}
	// 暫時英文期間在這個輸入框切換：之後就是共用的模式。
	do(a, proto.Request{Type: proto.Key, Key: proto.KeyEscape})
	do(c, proto.Request{Type: proto.Key, Key: proto.KeyEscape})
	do(a, proto.Request{Type: proto.Mode, On: true})
	if snap := do(a, proto.Request{Type: proto.Toggle}); snap.Mode != proto.ModeZhuyin {
		t.Fatalf("切回注音：%+v", snap)
	}
	do(b, proto.Request{Type: proto.Toggle})
	if snap := do(a, proto.Request{Type: proto.Key, Key: "a"}); snap.Mode != proto.ModeEnglish {
		t.Fatalf("自己切換過就照常跟上：%+v", snap)
	}
}
