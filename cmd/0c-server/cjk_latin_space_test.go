//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #273：settings.json 的 cjk_latin_space（缺欄位時是關）決定送出時要不要在漢字與英文字母之間補空格；
// health 回報引擎載入的值，設定程式據此比較「已儲存，尚未套用」。
// #365：cjk_digit_space 也是 true 時（「自動加空格」選「中英文與數字之間」）數字旁也補，health 另回報 cjk_digit_space；
// 只有它是 true、cjk_latin_space 是 false 的殘值照關閉。
func TestCJKLatinSpaceSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, tc := range []struct {
		file       map[string]any
		on, digits bool
		commit     string
	}{
		{map[string]any{"selection": "statistical"}, false, false, "我OK我2"},
		{map[string]any{"selection": "statistical", "cjk_latin_space": true}, true, false, "我 OK 我2"},
		{map[string]any{"selection": "statistical", "cjk_latin_space": true, "cjk_digit_space": true}, true, true, "我 OK 我 2"},
		{map[string]any{"selection": "statistical", "cjk_latin_space": false, "cjk_digit_space": true}, false, false, "我OK我2"},
	} {
		b, _ := json.Marshal(tc.file)
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "space.sock")
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock, "-legacy-socket", "")
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["cjk_latin_space"] != fmt.Sprint(tc.on) || health.Info["cjk_digit_space"] != fmt.Sprint(tc.digits) {
				t.Fatalf("%v 的 health：%+v", tc.file, health.Info)
			}
			open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
			do := func(req proto.Request) *proto.Snapshot {
				return call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &req}).Snapshot
			}
			for _, k := range []string{"j", "i", "3"} { // ㄨㄛˇ 我
				do(proto.Request{Type: proto.Key, Key: k})
			}
			do(proto.Request{Type: proto.Toggle})
			do(proto.Request{Type: proto.Key, Key: "O"})
			do(proto.Request{Type: proto.Key, Key: "K"})
			do(proto.Request{Type: proto.Toggle})
			for _, k := range []string{"j", "i", "3"} {
				do(proto.Request{Type: proto.Key, Key: k})
			}
			do(proto.Request{Type: proto.Toggle})
			do(proto.Request{Type: proto.Key, Key: "2"})
			snap := do(proto.Request{Type: proto.Key, Key: proto.KeyEnter})
			if snap == nil || snap.Commit != tc.commit {
				t.Fatalf("%v：%+v，要送出 %q", tc.file, snap, tc.commit)
			}
		}()
	}
}
