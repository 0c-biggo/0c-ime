//go:build linux

package main

import (
	"net"
	"testing"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/proto"
	"github.com/godbus/dbus/v5"
)

// serveWith 以指定的引擎資訊（health／open 回覆的 Info）啟動測試引擎。
func serveWith(t *testing.T, sock string, info map[string]string, shared bool) func() {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	eng := toyEngine(t)
	eng.SharedMode = shared
	go (&ipc.Server{Engine: eng, Info: info}).Serve(l)
	return func() { l.Close() }
}

// fakeClock 讓單按 Shift 的計時可控。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
func useFakeClock(e *engine) *fakeClock {
	c := &fakeClock{t: time.Unix(1000, 0)}
	e.svc.clock = c.now
	return c
}

// holdShift 按住 Shift d 之後放開，回傳注／英有沒有切換。
func holdShift(e *engine, c *fakeClock, key uint32, d time.Duration) bool {
	before := e.english
	e.processKey(key, 0, 0)
	c.advance(d)
	e.processKey(key, maskShift, maskRelease)
	return e.english != before
}

func TestShiftKeysFrom(t *testing.T) {
	for _, c := range []struct {
		name        string
		info        map[string]string
		left, right bool
		tap         time.Duration
	}{
		{"舊引擎（沒有 right_shift）", map[string]string{"left_shift": "true"}, true, true, 300 * time.Millisecond},
		{"right", map[string]string{"left_shift": "false", "right_shift": "true", "toggle_key": "right", "shift_tap_ms": "300"}, false, true, 300 * time.Millisecond},
		{"left", map[string]string{"left_shift": "true", "right_shift": "false", "shift_tap_ms": "250"}, true, false, 250 * time.Millisecond},
		{"none", map[string]string{"left_shift": "false", "right_shift": "false"}, false, false, 300 * time.Millisecond},
		{"看不懂的門檻", map[string]string{"shift_tap_ms": "x"}, false, true, 300 * time.Millisecond},
		{"引擎不可用", nil, false, true, 300 * time.Millisecond},
	} {
		k := shiftKeysFrom(c.info)
		if k.left != c.left || k.right != c.right || k.tap != c.tap {
			t.Errorf("%s：%+v，要 左=%v 右=%v 門檻=%v", c.name, k, c.left, c.right, c.tap)
		}
	}
}

// #117：按下到放開超過門檻（預設 0.3 秒）不算單按，左右 Shift 都一樣。
func TestShiftTapThreshold(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer serveWith(t, sock, map[string]string{"left_shift": "true", "right_shift": "true", "shift_tap_ms": "300"}, false)()
	c := useFakeClock(e)
	for _, key := range []uint32{keyShiftR, keyShiftL} {
		if holdShift(e, c, key, 500*time.Millisecond) || e.english {
			t.Fatalf("%#x：按住 0.5 秒放開不應切換", key)
		}
		if !holdShift(e, c, key, 300*time.Millisecond) || !e.english {
			t.Fatalf("%#x：按住 0.3 秒以內放開應切換", key)
		}
		if !holdShift(e, c, key, 50*time.Millisecond) || e.english {
			t.Fatalf("%#x：快按應切回注音", key)
		}
	}
	// GNOME Text Editor 的重複事件：從第一次按下起算。
	e.processKey(keyShiftR, 0, 0)
	c.advance(200 * time.Millisecond)
	e.processKey(keyShiftR, 0, 0)
	c.advance(200 * time.Millisecond)
	if e.processKey(keyShiftR, maskShift, maskRelease) || e.english {
		t.Fatal("重複事件也從第一次按下起算，合計 0.4 秒不應切換")
	}
	e.processKey(keyShiftR, maskShift, maskRelease)
	// 引擎回報較長的門檻時照它。
	stop := serveWith(t, sock+"2", map[string]string{"shift_tap_ms": "600"}, false)
	defer stop()
	e2, _, _ := newTestEngine(t)
	e2.svc.client.socket = sock + "2"
	c2 := useFakeClock(e2)
	if !holdShift(e2, c2, keyShiftR, 500*time.Millisecond) || !e2.english {
		t.Fatal("門檻 600 ms 時按住 0.5 秒應切換")
	}
}

// #117：中英切換鍵不含右 Shift（toggle-key=left／none）時，右 Shift 照系統原本的用途交給應用程式。
func TestToggleKeySides(t *testing.T) {
	for _, c := range []struct {
		toggleKey   string
		left, right bool
		label       string
	}{
		{"right", false, true, "注音（右 Shift 切換英文）"},
		{"both", true, true, "注音（Shift 切換英文）"},
		{"left", true, false, "注音（左 Shift 切換英文）"},
		{"none", false, false, "注音"},
	} {
		e, _, sock := newTestEngine(t)
		stop := serveWith(t, sock, map[string]string{"toggle_key": c.toggleKey,
			"left_shift": boolText(c.left), "right_shift": boolText(c.right), "shift_tap_ms": "300"}, false)
		typeKeys(e, "ji3")
		if got := tapRightShift(e); got != c.right || e.english != c.right {
			t.Fatalf("%s：右 Shift 單按 toggled=%v english=%v", c.toggleKey, got, e.english)
		}
		if e.english {
			tapRightShift(e)
		}
		e.processKey(keyShiftL, 0, 0)
		if got := e.processKey(keyShiftL, maskShift, maskRelease); got || e.english != c.left {
			t.Fatalf("%s：左 Shift 單按 handled=%v english=%v", c.toggleKey, got, e.english)
		}
		if e.english {
			e.processKey(keyShiftL, 0, 0)
			e.processKey(keyShiftL, maskShift, maskRelease)
		}
		if p := e.modeProperty(); p.label != c.label {
			t.Fatalf("%s：選單說明 %q，要 %q", c.toggleKey, p.label, c.label)
		}
		if e.preedit != "我" {
			t.Fatalf("%s：不切換的 Shift 不應動到組字：%q", c.toggleKey, e.preedit)
		}
		stop()
	}
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func focus(e *engine, in bool) {
	e.svc.refreshed = time.Now() // 不在測試裡啟動 0c-update 查詢狀態
	member := "FocusOut"
	if in {
		member = "FocusIn"
	}
	e.svc.engineMethod(e, member, nil)
}

// mode-memory=native：換輸入框（GNOME Wayland 上只看得到同一個輸入框的 FocusOut／FocusIn）回到注音。
func TestFocusInReturnsToZhuyin(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer serveWith(t, sock, map[string]string{"mode_memory": "native"}, false)()
	e.active = true
	if !tapRightShift(e) || !e.english {
		t.Fatal("右 Shift 應切到英文")
	}
	focus(e, false)
	focus(e, true)
	if e.english {
		t.Fatal("native：重新取得焦點應回到注音")
	}
}

// mode-memory=shared（#115）：一個輸入框切到英文，換到另一個輸入框時立刻是英文；換回 native 的引擎後回到注音。
func TestSharedModeFollowsAcrossContexts(t *testing.T) {
	a, _, sock := newTestEngine(t)
	stop := serveWith(t, sock, map[string]string{"mode_memory": "shared"}, true)
	b := &engine{svc: a.svc, path: enginePrefix + "2", session: newSession(a.svc.client, false)}
	a.svc.engines = map[dbus.ObjectPath]*engine{a.path: a, b.path: b}
	focus(b, true)
	typeKeys(b, "a")
	b.session.handle(proto.Request{Type: proto.Cancel})
	focus(b, false)
	focus(a, true)
	if !tapRightShift(a) || !a.english {
		t.Fatal("右 Shift 應切到英文")
	}
	focus(a, false)
	focus(b, true)
	if !b.english {
		t.Fatal("shared：換到另一個輸入框應立刻是英文")
	}
	focus(b, false)
	focus(a, true)
	if !a.english {
		t.Fatal("shared：回到原本的輸入框仍是英文")
	}
	// 套用 native（引擎重啟）：之後換輸入框回到注音。
	stop()
	a.svc.client.close()
	defer serveWith(t, sock, map[string]string{"mode_memory": "native"}, false)()
	typeKeys(a, "a")
	a.session.handle(proto.Request{Type: proto.Cancel})
	focus(a, false)
	focus(a, true)
	if a.english {
		t.Fatal("native：重新取得焦點應回到注音")
	}
}

// #165：切換鍵的按下與放開都要交給應用程式，切換了也一樣。GNOME Wayland 下 VS Code 等 Chromium／Electron
// 經 GNOME Shell 的 IBus 介面收鍵，只靠按鍵事件記 Shift：放開被吃掉就以為 Shift 一直按著，↑ 變成 Shift＋↑。
func TestShiftReleaseReachesApp(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer serveWith(t, sock, map[string]string{"toggle_key": "both",
		"left_shift": "true", "right_shift": "true", "shift_tap_ms": "300"}, false)()
	typeKeys(e, "ji3")
	for i, c := range []struct {
		key     uint32
		presses int // GTK4 程式把每個按放送兩次（8556892）
		english bool
	}{
		{keyShiftR, 1, true}, {keyShiftR, 1, false},
		{keyShiftL, 1, true}, {keyShiftL, 1, false},
		{keyShiftR, 2, true}, {keyShiftL, 2, false},
	} {
		for n := 0; n < c.presses; n++ {
			if e.processKey(c.key, 0, 0) {
				t.Fatalf("%d：%#x 按下被吃掉", i, c.key)
			}
		}
		for n := 0; n < c.presses; n++ {
			if e.processKey(c.key, maskShift, maskRelease) {
				t.Fatalf("%d：%#x 切換時放開被吃掉，應用程式會以為 Shift 沒放開", i, c.key)
			}
		}
		if e.english != c.english {
			t.Fatalf("%d：%#x 單按後 english=%v，要 %v", i, c.key, e.english, c.english)
		}
	}
	if e.preedit != "我" {
		t.Fatalf("切換應保留組字：%q", e.preedit)
	}
}
