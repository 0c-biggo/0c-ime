package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// #282：「所有符號鍵打半形」由引擎帶給每個 session；沒在組字時排列的標點鍵交還應用程式，idle_keys 不能列
// （Windows 的 TestKeyDown 照它決定要不要吃）。逐一比對排列、混打、Emoji 與各風格，答案要跟 session 實際的處理一致。
func TestAllSymbolsHalfwidthReachesSessionsAndIdleKeys(t *testing.T) {
	for _, l := range []layout.Layout{layout.Standard, layout.ETen, layout.Hsu} {
		for _, style := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
			for _, mixed := range []bool{false, true} {
				for _, emoji := range []string{"equals", "off"} {
					e := testEngine()
					p, _ := inputstyle.Get(style)
					e.InputStyle = p.Rules
					e.Layout, e.MixedInput, e.EmojiShortcut, e.Preview02 = l, mixed, emoji, true
					e.ShiftSymbolHalfwidth, e.AllSymbolsHalfwidth, e.CtrlPunct = true, true, true
					name := fmt.Sprintf("%s/%s/%v/%s", l.Name(), style, mixed, emoji)
					keys := e.IdleKeys()
					if l == layout.Standard && strings.ContainsAny(keys, "_(){}[]<>?:\"'\\!~&^") {
						t.Fatalf("%s：閒置的符號鍵要交還應用程式：%q", name, keys)
					}
					if !strings.Contains(keys, "`") {
						t.Fatalf("%s：反引號照舊由核心處理：%q", name, keys)
					}
					for r := rune(0x21); r <= 0x7e; r++ {
						id := e.OpenSession()
						snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: string(r)})
						if want := strings.ContainsRune(keys, r); snap.Handled != want {
							t.Errorf("%s：%q 實際 handled=%v，IdleKeys 回報 %v", name, r, snap.Handled, want)
						}
						e.CloseSession(id)
					}
				}
			}
		}
	}
	e := testEngine()
	e.ShiftSymbolHalfwidth, e.AllSymbolsHalfwidth, e.CtrlPunct = true, true, true
	id := e.OpenSession()
	styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeyCtrlComma})
	if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "["}); snap.Text() != "，[" {
		t.Fatalf("Ctrl＋, 打全形、[ 打半形：%+v", snap)
	}
}
