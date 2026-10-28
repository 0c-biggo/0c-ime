package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
)

var ctrlPunctKeys = []struct{ key, want string }{
	{proto.KeyCtrlComma, "，"}, {proto.KeyCtrlPeriod, "。"}, {proto.KeyCtrlQuote, "、"},
	{proto.KeyCtrlSemicolon, "；"}, {proto.KeyCtrlQuestion, "？"}, {proto.KeyCtrlExclaim, "！"},
	{proto.KeyCtrlColon, "："}, {proto.KeyCtrlParenLeft, "（"}, {proto.KeyCtrlParenRight, "）"}, // #231
}

func ctrlPunctSession() *Session { s := newSession(); s.CtrlPunct = true; return s }

// #168：中文模式的 Ctrl＋標點打全形，跟 Shift＋, 等一般標點一樣留在組字區；沒在組字時也一樣。
func TestCtrlPunctTypesFullWidthPunctuation(t *testing.T) {
	for _, tc := range ctrlPunctKeys {
		s := ctrlPunctSession()
		if snap := key(s, tc.key); !snap.Handled || snap.Commit != "" || snap.Text() != tc.want {
			t.Fatalf("%s 沒在組字時應插進組字區：%+v", tc.key, snap)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.want {
			t.Fatalf("%s：Enter 應送出 %q：%+v", tc.key, tc.want, snap)
		}
		typeSyl(s, "ㄨㄛˇ")
		if snap := key(s, tc.key); !snap.Handled || snap.Text() != "我"+tc.want {
			t.Fatalf("%s 組字中應接在後面：%+v", tc.key, snap)
		}
		if snap := typeSyl(s, "ㄒㄧㄤˇ"); snap.Text() != "我"+tc.want+"想" {
			t.Fatalf("%s 之後應能繼續打字：%+v", tc.key, snap)
		}
	}
	// 跟 Shift＋, 走同一條路：打出的組字相同。
	a, b := ctrlPunctSession(), ctrlPunctSession()
	typeSyl(a, "ㄨㄛˇ")
	typeSyl(b, "ㄨㄛˇ")
	if x, y := key(a, proto.KeyCtrlComma), key(b, "<"); x.Text() != y.Text() || x.Cursor != y.Cursor {
		t.Fatalf("Ctrl+, 應同 Shift+,：%+v / %+v", x, y)
	}
}

// 未打完的注音同一般標點留成符號；游標在中間時插在游標處。
func TestCtrlPunctKeepsPendingZhuyinAndCursor(t *testing.T) {
	s := ctrlPunctSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "v") // ㄒ
	if snap := key(s, proto.KeyCtrlPeriod); !snap.Handled || snap.Text() != "我ㄒ。" {
		t.Fatalf("未打完的注音應留成符號：%+v", snap)
	}
	s = ctrlPunctSession()
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄒㄧㄤˇ")
	key(s, proto.KeyLeft)
	if snap := key(s, proto.KeyCtrlComma); snap.Text() != "我，想" || snap.Cursor.Rune != 2 {
		t.Fatalf("應插在游標處：%+v", snap)
	}
}

// 設定關閉（零值）、英文模式：回未處理，外殼照一般快捷鍵先送出組字再交給應用程式（#106）。組字不變。
func TestCtrlPunctOffOrEnglishGoesToTheApp(t *testing.T) {
	for _, tc := range ctrlPunctKeys {
		s := newSession()
		if snap := key(s, tc.key); snap.Handled || snap.Text() != "" {
			t.Fatalf("設定關閉時 %s 應交還應用程式：%+v", tc.key, snap)
		}
		typeSyl(s, "ㄨㄛˇ")
		if snap := key(s, tc.key); snap.Handled || snap.Text() != "我" {
			t.Fatalf("設定關閉時組字中的 %s 應交還應用程式、組字不變：%+v", tc.key, snap)
		}
		s = ctrlPunctSession()
		s.Handle(proto.Request{Type: proto.Mode, On: true})
		if snap := key(s, tc.key); snap.Handled {
			t.Fatalf("英文模式的 %s 應交還應用程式：%+v", tc.key, snap)
		}
		s = ctrlPunctSession()
		typeSyl(s, "ㄨㄛˇ")
		s.Handle(proto.Request{Type: proto.Mode, On: true})
		if snap := key(s, tc.key); snap.Handled || snap.Text() != "我" {
			t.Fatalf("英文模式保留的組字中 %s 應交還應用程式：%+v", tc.key, snap)
		}
	}
	// 其他 Ctrl 鍵名不是協定的按鍵。
	s := ctrlPunctSession()
	for _, k := range []string{"Ctrl+/", "Ctrl+1", "Ctrl+[", "Ctrl+<"} {
		if snap := key(s, k); snap.Handled {
			t.Fatalf("%s 不是 Ctrl＋標點：%+v", k, snap)
		}
	}
}

// 候選開著時同其他按鍵吃掉不動作，不與選字同時解讀。
func TestCtrlPunctWithCandidatesOpenIsSwallowed(t *testing.T) {
	s := ctrlPunctSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, proto.KeyDown)
	snap := key(s, proto.KeyCtrlComma)
	if !snap.Handled || snap.Candidates == nil || snap.Text() != "我" {
		t.Fatalf("候選開著時應吃掉不動作：%+v", snap)
	}
}

// 中英混打：打到一半的英文先定案，再接全形標點（Shift＋, 在原樣英文後是半形 <）。
func TestCtrlPunctAfterMixedEnglish(t *testing.T) {
	s := mixedSession()
	s.CtrlPunct = true
	for _, k := range []string{"o", "k"} {
		key(s, k)
	}
	snap := key(s, proto.KeyCtrlComma)
	if !snap.Handled || snap.Text() != "ok，" {
		t.Fatalf("混打英文後應接全形逗號：%+v", snap)
	}
	if snap = key(s, proto.KeyEnter); snap.Commit != "ok，" {
		t.Fatalf("送出：%+v", snap)
	}
}

// 倚天 41 鍵的 ' 是 ㄘ，Ctrl+' 仍是頓號。
func TestCtrlPunctETen(t *testing.T) {
	s := ctrlPunctSession()
	s.SetLayout(layout.ETen)
	if snap := key(s, proto.KeyCtrlQuote); !snap.Handled || snap.Text() != "、" {
		t.Fatalf("倚天排列的 Ctrl+' 應是頓號：%+v", snap)
	}
}
