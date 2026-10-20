//go:build linux

package main

import (
	"net"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/godbus/dbus/v5"
)

func serveStartEnglish(t *testing.T, sock string, memory string) func() {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	eng := toyEngine(t)
	eng.SharedMode, eng.StartEnglish = memory == "shared", true
	go (&ipc.Server{Engine: eng, Info: map[string]string{"mode_memory": memory, "start_mode": "english"}}).Serve(l)
	return func() { l.Close() }
}

// #160：開始時是英文、mode-memory=native。第一個按鍵就是英文；切到注音後換輸入框回到英文（開始時的模式），
// 引擎已連上時新的輸入框一取得焦點就是英文（注／英指示不必等第一個按鍵）。
func TestStartEnglishNative(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer serveStartEnglish(t, sock, "native")()
	focus(e, true)
	if e.processKey('t', 0, 0) || !e.english {
		t.Fatalf("第一個按鍵就是英文、交還應用程式：english=%v", e.english)
	}
	e.processKey('t', 0, maskRelease)
	if !tapRightShift(e) || e.english {
		t.Fatal("右 Shift 切到注音")
	}
	focus(e, false)
	focus(e, true)
	if !e.english {
		t.Fatal("native：重新取得焦點回到開始時的英文")
	}
	b := &engine{svc: e.svc, path: enginePrefix + "2", session: newSession(e.svc.client, false)}
	e.svc.engines = map[dbus.ObjectPath]*engine{e.path: e, b.path: b}
	focus(e, false)
	focus(b, true)
	if !b.english {
		t.Fatal("新的輸入框一取得焦點就是英文")
	}
}

// #160：開始時是英文、mode-memory=shared。一開始是英文，切到注音後換輸入框維持注音（不回到開始時的模式）。
func TestStartEnglishShared(t *testing.T) {
	e, _, sock := newTestEngine(t)
	defer serveStartEnglish(t, sock, "shared")()
	focus(e, true)
	typeKeys(e, "t")
	if !e.english {
		t.Fatal("登入後一開始是英文")
	}
	e.session.handle(proto.Request{Type: proto.Cancel})
	if !tapRightShift(e) || e.english {
		t.Fatal("右 Shift 切到注音")
	}
	focus(e, false)
	focus(e, true)
	if e.english {
		t.Fatal("shared：換輸入框維持共用的注音")
	}
}
