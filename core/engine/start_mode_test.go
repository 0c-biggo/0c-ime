package engine

import (
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

func modeOf(t *testing.T, e *Engine, id uint64, r proto.Request) string {
	t.Helper()
	snap, err := e.Do(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return snap.Mode
}

// #160：開始時是英文。各自記住（native）時每個新 session 從英文開始，切換只影響自己；
// 第一個按鍵就是英文（字母交還應用程式），不是先組注音。
func TestStartEnglishEachSession(t *testing.T) {
	e := testEngine()
	e.StartEnglish = true
	if !e.InitialEnglish() {
		t.Fatal("新 session 的模式")
	}
	a := e.OpenSession()
	snap, _ := e.Do(a, proto.Request{Type: proto.Key, Key: "t"})
	if snap.Mode != proto.ModeEnglish || snap.Handled || len(snap.Preedit) != 0 {
		t.Fatalf("第一個按鍵就是英文：%+v", snap)
	}
	if modeOf(t, e, a, proto.Request{Type: proto.Toggle}) != proto.ModeZhuyin {
		t.Fatal("切回注音")
	}
	b := e.OpenSession()
	if modeOf(t, e, b, proto.Request{Type: proto.Key, Key: "t"}) != proto.ModeEnglish {
		t.Fatal("另一個新 session 仍從英文開始")
	}
	if modeOf(t, e, a, proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("切過的 session 維持自己的模式")
	}
	// 外殼明確設定模式（重連後還原、換輸入框回到開始時的模式）照它說的。
	if modeOf(t, e, b, proto.Request{Type: proto.Mode, On: false}) != proto.ModeZhuyin {
		t.Fatal("設成注音")
	}
	// 預設（零值）跟以前一樣從注音開始。
	z := testEngine()
	if z.InitialEnglish() || modeOf(t, z, z.OpenSession(), proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("預設注音")
	}
}

// #160：全部共用（shared）時，開始時的模式是引擎啟動（登入）後第一個共用的模式；之後跟著最後一次切換，
// 新 session 也跟上，InitialEnglish 回報目前共用的模式（外殼在第一個按鍵前據此顯示中／英）。
func TestStartEnglishShared(t *testing.T) {
	e := testEngine()
	e.SharedMode, e.StartEnglish = true, true
	a := e.OpenSession()
	if modeOf(t, e, a, proto.Request{Type: proto.Key, Key: "t"}) != proto.ModeEnglish {
		t.Fatal("登入後一開始是英文")
	}
	// 外殼換輸入框時的「設成注音」回到共用的模式（英文），不是注音。
	b := e.OpenSession()
	if modeOf(t, e, b, proto.Request{Type: proto.Mode, On: false}) != proto.ModeEnglish {
		t.Fatal("焦點重設回到共用的模式")
	}
	if modeOf(t, e, b, proto.Request{Type: proto.Toggle}) != proto.ModeZhuyin || e.InitialEnglish() {
		t.Fatal("切到注音後共用的模式是注音")
	}
	c := e.OpenSession()
	if modeOf(t, e, c, proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("切換後新開的 session 跟著共用的模式，不再回到開始時的英文")
	}
	if modeOf(t, e, a, proto.Request{Type: proto.Key, Key: "j"}) != proto.ModeZhuyin {
		t.Fatal("舊 session 也跟上")
	}
}
