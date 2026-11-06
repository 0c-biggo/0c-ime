package engine

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// #258：設定「Shift＋符號打半形」由引擎帶給每個 session；沒在組字時這些鍵交還應用程式，
// idle_keys 不能再列（Windows 的 TestKeyDown 照它決定要不要吃）。未按 Shift 的 [ ] 照舊。
func TestShiftSymbolHalfwidthReachesSessionsAndIdleKeys(t *testing.T) {
	e := testEngine()
	if !strings.ContainsAny(e.IdleKeys(), "(){}:") {
		t.Fatalf("預設 Shift 符號由核心打全形：%q", e.IdleKeys())
	}
	e.ShiftSymbolHalfwidth = true
	if strings.ContainsAny(e.IdleKeys(), "(){}:?!~&^_") {
		t.Fatalf("Shift＋符號打半形時不能吃半形就是鍵帽字元的閒置 Shift 符號：%q", e.IdleKeys())
	}
	if !strings.Contains(e.IdleKeys(), "<") || !strings.Contains(e.IdleKeys(), ">") || !strings.Contains(e.IdleKeys(), "\"") {
		t.Fatalf("< > \" 由核心送出半形 , . ;，閒置時也要吃：%q", e.IdleKeys())
	}
	if !strings.Contains(e.IdleKeys(), "[") || !strings.Contains(e.IdleKeys(), "`") {
		t.Fatalf("[ 與 ` 照舊由核心處理：%q", e.IdleKeys())
	}
	id := e.OpenSession()
	if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "("}); snap.Handled {
		t.Fatalf("閒置的 ( 交還應用程式：%+v", snap)
	}
	if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "<"}); !snap.Handled || snap.Commit != "," {
		t.Fatalf("閒置的 Shift＋, 直接送出半形逗號：%+v", snap)
	}
	if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "["}); !snap.Handled || snap.Text() != "「" {
		t.Fatalf("[ 照舊打「：%+v", snap)
	}
	if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "("}); !snap.Handled || snap.Text() != "「(" {
		t.Fatalf("組字中的 ( 是半形：%+v", snap)
	}
}

// 微軟參考風格本來把 " < > 交還應用程式；開了設定時它們打 ; , .，閒置時要由核心送出。
func TestShiftSymbolHalfwidthMicrosoftIdleKeys(t *testing.T) {
	e := testEngine()
	p, _ := inputstyle.Get("microsoft")
	e.InputStyle = p.Rules
	e.ShiftSymbolHalfwidth = true
	keys := e.IdleKeys()
	if !strings.Contains(keys, "<") || !strings.Contains(keys, ">") || !strings.Contains(keys, "\"") || strings.ContainsAny(keys, "\\'?(") {
		t.Fatalf("微軟風格＋Shift＋符號打半形的閒置按鍵：%q", keys)
	}
	id := e.OpenSession()
	if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: ">"}); !snap.Handled || snap.Commit != "." {
		t.Fatalf("閒置的 Shift＋. 送出半形句號：%+v", snap)
	}
}
