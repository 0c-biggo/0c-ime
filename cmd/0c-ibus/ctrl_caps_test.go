//go:build linux

package main

import (
	"net"
	"testing"

	"git.biggo.com/0c/ime/bridge/ipc"
	core "git.biggo.com/0c/ime/core/engine"
)

func serveEngine(t *testing.T, sock string, eng *core.Engine, info map[string]string) func() {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go (&ipc.Server{Engine: eng, Info: info}).Serve(l)
	return func() { l.Close() }
}

func TestFoldCaps(t *testing.T) {
	for _, c := range []struct {
		keyval, state, want uint32
	}{
		{'J', maskLock, 'j'},             // Caps Lock：大寫字母換回小寫注音鍵
		{'j', maskLock | maskShift, 'J'}, // Caps Lock＋Shift：照沒開 Caps Lock 時的 Shift＋字母，大寫
		{'J', maskShift, 'J'},            // 沒開 Caps Lock 不動
		{'j', 0, 'j'},
		{'3', maskLock, '3'}, // 數字、標點不受 Caps Lock 影響
		{',', maskLock, ','},
		{keyReturn, maskLock, keyReturn},
	} {
		if got := foldCaps(c.keyval, c.state); got != c.want {
			t.Errorf("foldCaps(%q, %#x) = %q，要 %q", rune(c.keyval), c.state, rune(got), rune(c.want))
		}
	}
}

func TestCtrlPunctKey(t *testing.T) {
	for _, c := range []struct {
		keyval, state uint32
		want          string
	}{
		{',', maskControl, "Ctrl+,"},
		{'.', maskControl, "Ctrl+."},
		{'\'', maskControl, "Ctrl+'"},
		{';', maskControl, "Ctrl+;"},
		{'?', maskControl | maskShift, "Ctrl+?"},
		{'!', maskControl | maskShift, "Ctrl+!"},
		{',', maskControl | maskLock, "Ctrl+,"},
		{',', 0, ""},                       // 沒按 Ctrl
		{',', maskControl | maskMod1, ""},  // Ctrl+Alt
		{',', maskControl | maskSuper, ""}, // Ctrl+Super
		{'/', maskControl, ""},             // Ctrl+/（VS Code 註解）照舊交給應用程式
		{'1', maskControl, ""},             // Ctrl+1
		{'<', maskControl | maskShift, ""}, // Ctrl+Shift+,
		{'[', maskControl, ""},             // 不做 Ctrl+[ ]
		{'v', maskControl, ""},
	} {
		if got := ctrlPunctKey(c.keyval, c.state); got != c.want {
			t.Errorf("ctrlPunctKey(%q, %#x) = %q，要 %q", rune(c.keyval), c.state, got, c.want)
		}
	}
}

// #168：中文模式不理 Caps Lock；英文模式照舊交給應用程式打大寫。
func TestCapsLockIgnoredInZhuyinMode(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer startServer(t, sock, toyEngine(t))()
	for _, k := range "JI3" {
		e.processKey(uint32(k), 0, maskLock)
		e.processKey(uint32(k), 0, maskLock|maskRelease)
	}
	if e.preedit != "我" {
		t.Fatalf("Caps Lock 開著打 ji3 應是「我」：%q", e.preedit)
	}
	// Caps Lock＋Shift＋字母照沒開 Caps Lock 時的 Shift＋字母：組字中是大寫原樣字母。
	if !e.processKey('j', 0, maskLock|maskShift) || e.preedit != "我J" {
		t.Fatalf("Shift＋字母應是大寫：%q", e.preedit)
	}
	e.processKey(keyEscape, 0, 0)
	e.processKey(keyEscape, 0, 0)
	if !tapRightShift(e) || !e.english {
		t.Fatal("右 Shift 應切到英文")
	}
	if e.processKey('A', 0, maskLock) || e.preedit != "" {
		t.Fatalf("英文模式的 Caps Lock 字母應交給應用程式：%q", e.preedit)
	}
}

// #168：引擎回報 ctrl_punctuation 時中文模式的 Ctrl＋標點打全形；英文模式、設定關閉或舊引擎照舊先送出組字再交給應用程式。
func TestCtrlPunctuationKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		info map[string]string
		on   bool
	}{{"開", map[string]string{"ctrl_punctuation": "true"}, true}, {"關", map[string]string{"ctrl_punctuation": "false"}, false}, {"舊引擎", map[string]string{}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, sock := newTestEngine(t)
			eng := toyEngine(t)
			eng.CtrlPunct = tc.on
			defer serveEngine(t, sock, eng, tc.info)()
			if got := e.processKey(',', 0, maskControl); got != tc.on || (tc.on && e.preedit != "，") {
				t.Fatalf("沒在組字的 Ctrl+,：%v %q", got, e.preedit)
			}
			typeKeys(e, "ji3")
			before := e.preedit
			got := e.processKey('!', 0, maskControl|maskShift)
			if tc.on && (!got || e.preedit != before+"！") {
				t.Fatalf("組字中 Ctrl+Shift+1 應接全形驚嘆號：%v %q", got, e.preedit)
			}
			if !tc.on && (got || e.preedit != "") {
				t.Fatalf("關閉時 Ctrl+Shift+1 應先送出組字再交給應用程式：%v %q", got, e.preedit)
			}
			// Ctrl+/ 不是 Ctrl＋標點：先送出組字再交給應用程式。
			typeKeys(e, "ji3")
			if e.processKey('/', 0, maskControl) || e.preedit != "" {
				t.Fatalf("Ctrl+/ 應交給應用程式：%q", e.preedit)
			}
			// 英文模式交給應用程式（保留的組字先送出）。
			typeKeys(e, "ji3")
			tapRightShift(e)
			if !e.english || e.processKey('.', 0, maskControl) || e.preedit != "" {
				t.Fatalf("英文模式的 Ctrl+. 應先送出組字再交給應用程式：%v %q", e.english, e.preedit)
			}
		})
	}
}
