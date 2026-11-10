package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// #278：Shift＋空白切到全形後，Windows 的 TestKeyDown 照 idle_keys_full_width 決定沒在組字時要不要吃。
// 答案必須跟 session 實際的處理一致：說要吃卻回未處理，WPF 會掉鍵；說不吃，@ # * 就交給應用程式打成半形。
// 逐一比對排列、混打、聲調選填、Emoji、各風格的標點與 Shift＋字母、「Shift＋符號打半形」的組合。
func TestIdleKeysFullWidthMatchSession(t *testing.T) {
	for _, l := range []layout.Layout{layout.Standard, layout.ETen, layout.Hsu} {
		for _, style := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
			for _, letter := range []string{"", "inline", "commit", "lower"} {
				for _, opts := range []struct{ mixed, toneless, halfShift bool }{
					{false, false, false}, {true, false, false}, {true, true, false}, {false, false, true}, {true, false, true},
				} {
					for _, emoji := range []string{"equals", "off"} {
						e := testEngine()
						p, _ := inputstyle.Get(style)
						e.InputStyle = p.Rules
						e.InputStyle.ShiftSpace = "fullwidth"
						if letter != "" {
							e.InputStyle.ShiftLetter = letter
						}
						e.Layout, e.MixedInput, e.Toneless, e.ShiftSymbolHalfwidth, e.EmojiShortcut = l, opts.mixed, opts.toneless, opts.halfShift, emoji
						e.Preview02 = true
						name := fmt.Sprintf("%s/%s/%s/%+v/%s", l.Name(), style, letter, opts, emoji)
						keys := e.IdleKeysFullWidth()
						fullWidthSession := func() uint64 {
							id := e.OpenSession()
							if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeyShiftSpace}); !snap.FullWidth || snap.Text() != "" || snap.Commit != "" {
								t.Fatalf("%s：Shift＋空白沒切到全形：%+v", name, snap)
							}
							return id
						}
						for r := rune(0x21); r <= 0x7e; r++ {
							id := fullWidthSession()
							snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: string(r)})
							if want := strings.ContainsRune(keys, r); snap.Handled != want {
								t.Errorf("%s：全形 %q 實際 handled=%v，idle_keys_full_width 回報 %v", name, r, snap.Handled, want)
							}
							e.CloseSession(id)
						}
						// 數字鍵盤全形時一律打全形（外殼看字寬，不看清單）。
						for _, c := range "0123456789*+-./" {
							id := fullWidthSession()
							if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeypadPrefix + string(c)}); !snap.Handled {
								t.Errorf("%s：全形數字鍵盤 %q 沒有處理：%+v", name, c, snap)
							}
							e.CloseSession(id)
						}
					}
				}
			}
		}
	}
}

// 客戶的設定（我的原有設定、標準排列、Shift＋空白切全形，#278）：@ # * + 與數字鍵盤都要打全形，
// 注音鍵與標點照舊；閒置的聲調鍵照半形時的答案。
func TestIdleKeysFullWidthLegacyStandard(t *testing.T) {
	e := testEngine()
	p, _ := inputstyle.Get("0c")
	e.InputStyle = p.Rules
	e.InputStyle.ShiftSpace = "fullwidth"
	e.EnglishAutocomplete, e.Preview02 = true, true
	keys := e.IdleKeysFullWidth()
	for _, k := range "@#$%*+=|ABZjm,.;[" {
		if !strings.ContainsRune(keys, k) {
			t.Errorf("全形時 %q 要由引擎處理：%q", k, keys)
		}
	}
	for _, r := range e.IdleKeys() {
		if !strings.ContainsRune(keys, r) {
			t.Errorf("半形會處理的 %q 全形也要處理：%q", r, keys)
		}
	}
	id := e.OpenSession()
	styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeyShiftSpace})
	for k, want := range map[string]string{"@": "＠", "*": "＊", "#": "＃", "+": "＋", "KP1": "１", "KP*": "＊"} {
		if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: k}); !snap.Handled || snap.Commit != want {
			t.Errorf("全形 %q：%+v", k, snap)
		}
	}
}
