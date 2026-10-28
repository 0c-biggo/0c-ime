//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #173：settings.json 的 caps_lock_english（缺欄位時預設關）決定 caps_lock 請求有沒有作用；health 與 open 的 info 回報
// 引擎載入的值，外殼據此決定要不要回報 Caps Lock 狀態，設定程式據此比較「已儲存，尚未套用」。
// macOS 沒有這個選項：設定檔寫了也不改 caps_lock_english；開 Caps Lock 一律是英文，引擎一律處理 caps_lock（#219），
// 由 caps_lock_request 告訴外殼改送 caps_lock，關閉時回到開啟前的模式。
func TestCapsLockEnglishSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, tc := range []struct {
		file map[string]any
		on   bool
	}{{map[string]any{"selection": "statistical"}, false}, {map[string]any{"selection": "statistical", "caps_lock_english": true}, true}} {
		b, _ := json.Marshal(tc.file)
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "caps.sock")
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock, "-legacy-socket", "")
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		tc.on = tc.on && settings.CapsLockEnglishSetting(runtime.GOOS)
		handles := tc.on || runtime.GOOS == "darwin"
		func() {
			defer func() { server.Process.Signal(os.Interrupt); server.Wait() }()
			waitDial(t, sock)
			conn, err := net.Dial("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			call := func(env ipc.Envelope) ipc.Reply {
				t.Helper()
				if err := ipc.WriteFrame(conn, env); err != nil {
					t.Fatal(err)
				}
				var r ipc.Reply
				if err := ipc.ReadFrame(conn, &r); err != nil {
					t.Fatal(err)
				}
				return r
			}
			want := map[bool]string{false: "false", true: "true"}[tc.on]
			wantRequest := map[bool]string{false: "false", true: "true"}[handles]
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["caps_lock_english"] != want || health.Info["caps_lock_request"] != wantRequest {
				t.Fatalf("health：%+v", health.Info)
			}
			open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
			if open.Info["caps_lock_english"] != want || open.Info["caps_lock_request"] != wantRequest {
				t.Fatalf("open：%+v", open.Info)
			}
			req := proto.Request{Type: proto.CapsLock, On: true}
			snap := call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &req}).Snapshot
			wantMode := map[bool]string{false: proto.ModeZhuyin, true: proto.ModeEnglish}[handles]
			if snap == nil || snap.Handled != handles || snap.Mode != wantMode || snap.CapsLock != handles {
				t.Fatalf("caps_lock_english=%v：%+v", tc.on, snap)
			}
		}()
	}
}
