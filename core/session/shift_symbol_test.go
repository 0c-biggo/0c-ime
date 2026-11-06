package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
)

// #258：設定「Shift＋符號打半形」開啟時，注音模式的 Shift＋符號鍵打它原本那個全形標點的半形版本
// （Shift＋, 的「，」→ ,），全形沒有對應的半形 ASCII 時（『』……——）才打鍵帽字元；全形標點改用 Ctrl＋符號（#168）。
// 未按 Shift 的 [ ] ' \ 照舊打「」、，↓ 第二項是半形原字元。

// halfShiftKeys 是標準排列裡有全形標點的 Shift 符號鍵（standardPunct）。
var halfShiftKeys = []string{"<", ">", "?", ":", "\"", "!", "~", "{", "}", "(", ")", "&", "^", "_"}

// halfShiftWant：設定開啟後每個 Shift 符號鍵打出的字元（三種排列相同）。
var halfShiftWant = map[string]string{
	"<": ",", ">": ".", "?": "?", ":": ":", "\"": ";", "!": "!", "~": "~",
	"{": "{", "}": "}", "(": "(", ")": ")", "&": "&", "^": "^", "_": "_",
}

func halfShiftSession() *Session {
	s := newSession()
	s.ShiftSymbolHalfwidth = true
	s.CtrlPunct = true
	return s
}

func TestShiftSymbolHalfwidthOffKeepsFullWidth(t *testing.T) {
	for _, k := range halfShiftKeys {
		s := newSession()
		p, _ := layout.Standard.Punct([]rune(k)[0])
		if snap := key(s, k); !snap.Handled || snap.Text() != p {
			t.Fatalf("設定關閉時 %q 照舊打 %q：%+v", k, p, snap)
		}
	}
}

// 沒在組字時：半形就是鍵帽字元的交還應用程式（同 @ # 等本來就是半形的符號），不同的（< > "）由核心直接送出；
// 組字中插在游標處、留在組字區。
func TestShiftSymbolHalfwidthTypesTheKey(t *testing.T) {
	for _, k := range halfShiftKeys {
		half := halfShiftWant[k]
		for _, l := range []layout.Layout{layout.Standard, layout.ETen, layout.Hsu} {
			if p, _ := l.Punct([]rune(k)[0]); halfwidthPunct(p, k) != half {
				t.Fatalf("%s 的 %q（%q）應打 %q", l.Name(), k, p, half)
			}
		}
		s := halfShiftSession()
		snap := key(s, k)
		if half == k && (snap.Handled || snap.Text() != "" || snap.Commit != "") {
			t.Fatalf("沒在組字時 %q 應交還應用程式：%+v", k, snap)
		}
		if half != k && (!snap.Handled || snap.Text() != "" || snap.Commit != half) {
			t.Fatalf("沒在組字時 %q 應直接送出 %q：%+v", k, half, snap)
		}
		typeSyl(s, "ㄨㄛˇ")
		if snap := key(s, k); !snap.Handled || snap.Commit != "" || snap.Text() != "我"+half {
			t.Fatalf("組字中 %q 應以半形 %q 留在組字區：%+v", k, half, snap)
		}
		if snap := typeSyl(s, "ㄒㄧㄤˇ"); snap.Text() != "我"+half+"想" {
			t.Fatalf("%q 之後應能繼續打字：%+v", k, snap)
		}
		if snap := key(s, proto.KeyEnter); snap.Commit != "我"+half+"想" {
			t.Fatalf("Enter 送出 %q：%+v", k, snap)
		}
	}
	// 未打完的注音同全形標點留成符號；游標在中間時插在游標處。
	s := halfShiftSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, "v") // ㄒ
	if snap := key(s, "("); snap.Text() != "我ㄒ(" {
		t.Fatalf("未打完的注音應留成符號：%+v", snap)
	}
	s = halfShiftSession()
	typeSyl(s, "ㄨㄛˇ")
	typeSyl(s, "ㄒㄧㄤˇ")
	key(s, proto.KeyLeft)
	if snap := key(s, ":"); snap.Text() != "我:想" || snap.Cursor.Rune != 2 {
		t.Fatalf("應插在游標處：%+v", snap)
	}
}

// ↓ 第二項是全形（原本這一鍵打的標點），選得回去；鍵帽字元跟半形不同時（< > "）也列在後面，< > 仍打得到。
func TestShiftSymbolHalfwidthDownOffersFullWidth(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want []string
	}{
		{"(", []string{"(", "（"}}, {"<", []string{",", "，", "<"}}, {">", []string{".", "。", ">"}}, {"\"", []string{";", "；", "\""}},
		{"?", []string{"?", "？"}}, {"{", []string{"{", "『"}}, {"&", []string{"&", "＆", "﹠"}}, {"^", []string{"^", "……"}}, {"_", []string{"_", "——"}},
	} {
		s := halfShiftSession()
		typeSyl(s, "ㄨㄛˇ")
		key(s, tc.key)
		key(s, proto.KeyDown)
		got := candidateTexts(s)
		if len(got) < len(tc.want) || !reflect.DeepEqual(got[:len(tc.want)], tc.want) {
			t.Fatalf("%q 的 ↓ 應以 %q 開頭：%q", tc.key, tc.want, got)
		}
		for _, w := range tc.want {
			if strings.Count(strings.Join(got, "\x00")+"\x00", w+"\x00") != 1 {
				t.Fatalf("%q 的 ↓ 不應重複列 %q：%q", tc.key, w, got)
			}
		}
		key(s, "2")
		if snap := key(s, proto.KeyEnter); snap.Commit != "我"+tc.want[1] {
			t.Fatalf("選第二項應換成全形 %q：%+v", tc.want[1], snap)
		}
		if len(tc.want) > 2 && tc.want[2] == tc.key {
			s = halfShiftSession()
			typeSyl(s, "ㄨㄛˇ")
			key(s, tc.key)
			key(s, proto.KeyDown)
			key(s, "3")
			if snap := key(s, proto.KeyEnter); snap.Commit != "我"+tc.key {
				t.Fatalf("選第三項應換成鍵帽字元 %q：%+v", tc.key, snap)
			}
		}
	}
}

// 未按 Shift 的 [ ] ' \ 照舊打全形；設定開著時 ↓ 第二項是半形原字元。
func TestShiftSymbolHalfwidthUnshiftedKeysStayFullWidth(t *testing.T) {
	for _, tc := range []struct{ key, full string }{{"[", "「"}, {"]", "」"}, {"'", "、"}, {"\\", "、"}} {
		s := halfShiftSession()
		if snap := key(s, tc.key); !snap.Handled || snap.Text() != tc.full {
			t.Fatalf("%q 照舊打 %q：%+v", tc.key, tc.full, snap)
		}
		key(s, proto.KeyDown)
		if got := candidateTexts(s); len(got) < 2 || got[0] != tc.full || got[1] != tc.key {
			t.Fatalf("%q 的 ↓ 第二項應是半形 %q：%q", tc.full, tc.key, got)
		}
		key(s, "2")
		if snap := key(s, proto.KeyEnter); snap.Commit != tc.key {
			t.Fatalf("選第二項應換成半形 %q：%+v", tc.key, snap)
		}
		// 設定關閉時 ↓ 不附半形（#177）。
		s = newSession()
		key(s, tc.key)
		key(s, proto.KeyDown)
		if got := candidateTexts(s); len(got) > 1 && got[1] == tc.key {
			t.Fatalf("設定關閉時 %q 的 ↓ 不應附半形：%q", tc.full, got)
		}
	}
}

// Ctrl＋符號照舊打全形。
func TestShiftSymbolHalfwidthCtrlStaysFullWidth(t *testing.T) {
	for _, tc := range ctrlPunctKeys {
		s := halfShiftSession()
		if snap := key(s, tc.key); !snap.Handled || snap.Text() != tc.want {
			t.Fatalf("%s 應照舊打 %q：%+v", tc.key, tc.want, snap)
		}
	}
}

// 英文模式、候選開著、全形模式（Shift＋空白切的全形）照舊。
func TestShiftSymbolHalfwidthOtherStatesUnchanged(t *testing.T) {
	s := halfShiftSession()
	s.Handle(proto.Request{Type: proto.Mode, On: true})
	if snap := key(s, "("); snap.Handled {
		t.Fatalf("英文模式交還應用程式：%+v", snap)
	}
	s = halfShiftSession()
	typeSyl(s, "ㄨㄛˇ")
	key(s, proto.KeyDown)
	before := candidateTexts(s)
	if snap := key(s, "("); !snap.Handled || snap.Text() != "我" || !reflect.DeepEqual(candidateTexts(s), before) {
		t.Fatalf("候選開著時吃掉不動作：%+v", snap)
	}
	for _, style := range []string{"microsoft", "chewing"} {
		a, b := styleSession(style), styleSession(style)
		b.ShiftSymbolHalfwidth = true
		for _, k := range []string{proto.KeyShiftSpace, "(", ")", "{", ":", proto.KeyEnter} {
			if sa, sb := key(a, k), key(b, k); !reflect.DeepEqual(sa, sb) {
				t.Fatalf("%s 全形模式的 %q 應照舊：%+v != %+v", style, k, sa, sb)
			}
		}
	}
	// 微軟參考的反引號前導照舊打全形。
	s = styleSession("microsoft")
	s.ShiftSymbolHalfwidth = true
	key(s, "`")
	if snap := key(s, "("); snap.Text() != "（" {
		t.Fatalf("反引號前導照舊全形：%+v", snap)
	}
}

// 中英混打：Shift 符號一律半形；閉括號不再跟著全形的開括號（設定就是要半形，全形用 Ctrl）。
// 未按 Shift 的 [ ] 照 #232 配對。英文後面的 : 照舊併進網址、路徑。
func TestShiftSymbolHalfwidthMixedInput(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"ji3(DNS)", "我(DNS)"},
		{"DNS(ji3)", "DNS(我)"},
		{"ji3{DNS}", "我{DNS}"},
		{"ji3<", "我,"},
		{"ji3>", "我."},
		{"DNS<ji3", "DNS,我"},
		{"ji3[DNS]", "我「DNS」"},
		{"DNS[ji3]", "DNS[我]"},
		{"ji3?", "我?"},
		{"ji3 3:00", "我 3:00"},
	} {
		s := mixedSession()
		s.ShiftSymbolHalfwidth = true
		mixedType(s, tc.keys)
		if got := key(s, proto.KeyEnter).Commit; got != tc.want {
			t.Errorf("%q 提交 %q，要 %q", tc.keys, got, tc.want)
		}
	}
	s := mixedSession()
	s.ShiftSymbolHalfwidth = true
	if snap := key(s, "("); snap.Handled {
		t.Fatalf("混打沒在組字時 ( 交還應用程式：%+v", snap)
	}
}

// 倚天、許氏：Shift 符號一樣打半形；許氏未按 Shift 的 , . ; 照舊打「，。；」。
func TestShiftSymbolHalfwidthOtherLayouts(t *testing.T) {
	for _, l := range []layout.Layout{layout.ETen, layout.Hsu} {
		s := hsuSession(t)
		s.SetLayout(l)
		s.ShiftSymbolHalfwidth = true
		key(s, "a")
		key(s, " ")
		if snap := key(s, "("); !strings.HasSuffix(snap.Text(), "(") {
			t.Fatalf("%s 的 ( 應是半形：%+v", l.Name(), snap)
		}
	}
	s := hsuSession(t)
	s.ShiftSymbolHalfwidth = true
	if snap := key(s, ","); snap.Text() != "，" {
		t.Fatalf("許氏的 , 照舊打「，」：%+v", snap)
	}
}

// 閒置按鍵（Windows 的 TestKeyDown）：設定開啟時從 IdleKeys 拿掉 HalfwidthShiftKeys，結果要跟 session 實際的處理一致。
func TestShiftSymbolHalfwidthIdleKeysMatchSession(t *testing.T) {
	for _, l := range []layout.Layout{layout.Standard, layout.ETen, layout.Hsu} {
		for _, mixed := range []bool{false, true} {
			drop := HalfwidthShiftKeys(l)
			idle := strings.Map(func(r rune) rune {
				if strings.ContainsRune(drop, r) {
					return -1
				}
				return r
			}, IdleKeys(l, mixed, "equals"))
			for r := rune(0x21); r <= 0x7e; r++ {
				s := hsuSession(t)
				s.SetLayout(l)
				s.MixedInput, s.EmojiShortcut, s.Preview02, s.ShiftSymbolHalfwidth = mixed, "equals", true, true
				got := key(s, string(r)).Handled
				if want := strings.ContainsRune(idle, r); got != want {
					t.Errorf("%s 混打=%v：%q 實際 handled=%v，IdleKeys 回報 %v", l.Name(), mixed, r, got, want)
				}
			}
		}
	}
	got := HalfwidthShiftKeys(layout.Standard)
	for _, k := range halfShiftKeys {
		if strings.Contains(got, k) != (halfShiftWant[k] == k) {
			t.Errorf("標準排列沒在組字時交還應用程式的 Shift 鍵（半形就是鍵帽字元）對 %q 不對：%q", k, got)
		}
	}
	if strings.ContainsAny(got, "[]',.;\\@#<>\"") {
		t.Errorf("未按 Shift 或本來就半形的鍵不算：%q", got)
	}
}

// 微軟參考風格的 \ ' " < > ? 本來就打半形原字元（microsoftRawPunctuation）。這個設定的意思是「Shift 組合不打全形」，
// 不是換成另一個字，所以開了也照風格（#272）：Shift＋, 仍是 <、Shift＋. 仍是 >、Shift＋' 仍是 "，閒置、組字中、
// ↓、中英混打都跟沒開時一樣。其他 Shift 符號（( ) { } : ! ~ & ^ _）照設定打半形；反引號前導照舊打全形。
func TestShiftSymbolHalfwidthKeepsMicrosoftRawKeys(t *testing.T) {
	same := func(name string, run func(s *Session) proto.Snapshot, after func(s *Session) []string) {
		t.Helper()
		off, on := styleSession("microsoft"), styleSession("microsoft")
		on.ShiftSymbolHalfwidth = true
		a, b := run(off), run(on)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("微軟風格%s開了設定應照風格：關 %+v，開 %+v", name, a, b)
		}
		if after != nil && !reflect.DeepEqual(after(off), after(on)) {
			t.Errorf("微軟風格%s的 ↓ 應照風格：關 %q，開 %q", name, after(off), after(on))
		}
	}
	for _, k := range []string{"<", ">", "\"", "?", "\\", "'"} {
		same("閒置 "+k, func(s *Session) proto.Snapshot { return key(s, k) }, nil)
		same("組字中 "+k, func(s *Session) proto.Snapshot { typeSyl(s, "ㄨㄛˇ"); key(s, k); return key(s, proto.KeyEnter) }, nil)
		same("組字中 ↓ "+k, func(s *Session) proto.Snapshot {
			typeSyl(s, "ㄨㄛˇ")
			key(s, k)
			key(s, proto.KeyLeft)
			return key(s, proto.KeyDown)
		}, candidateTexts)
		same("混打 "+k, func(s *Session) proto.Snapshot {
			s.MixedInput = true
			mixedType(s, "ji3"+k+"DNS"+k+"ji3")
			return key(s, proto.KeyEnter)
		}, nil)
	}
	s := styleSession("microsoft")
	s.ShiftSymbolHalfwidth = true
	if snap := key(s, "<"); snap.Handled {
		t.Fatalf("微軟風格閒置的 Shift＋, 照風格交還應用程式打 <：%+v", snap)
	}
	typeSyl(s, "ㄨㄛˇ")
	key(s, "<")
	key(s, ">")
	key(s, "\"")
	if snap := key(s, proto.KeyEnter); snap.Commit != "我<>\"" {
		t.Fatalf("微軟風格組字中 Shift＋, . ' 照風格打 < > \"：%+v", snap)
	}
	// 其他 Shift 符號照設定打半形。
	for _, k := range []string{"(", ")", "{", "}", ":", "!", "~", "&", "^", "_"} {
		s := styleSession("microsoft")
		s.ShiftSymbolHalfwidth = true
		typeSyl(s, "ㄨㄛˇ")
		key(s, k)
		if snap := key(s, proto.KeyEnter); snap.Commit != "我"+k {
			t.Errorf("微軟風格開了設定時 %q 打半形：%+v", k, snap)
		}
	}
	s = styleSession("microsoft")
	s.ShiftSymbolHalfwidth = true
	key(s, "`")
	if snap := key(s, "<"); snap.Text() != "＜" {
		t.Fatalf("反引號前導照舊全形：%+v", snap)
	}
}
