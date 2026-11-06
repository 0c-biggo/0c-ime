//go:build !windows

package main

import (
	"encoding/json"
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
func TestCJKLatinSpaceSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, tc := range []struct {
		file map[string]any
		on   bool
	}{{map[string]any{"selection": "statistical"}, false}, {map[string]any{"selection": "statistical", "cjk_latin_space": true}, true}} {
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
			want := map[bool]string{false: "false", true: "true"}[tc.on]
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["cjk_latin_space"] != want {
				t.Fatalf("health：%+v", health.Info)
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
			snap := do(proto.Request{Type: proto.Key, Key: proto.KeyEnter})
			if commit := map[bool]string{false: "我OK", true: "我 OK"}[tc.on]; snap == nil || snap.Commit != commit {
				t.Fatalf("cjk_latin_space=%v：%+v，要送出 %q", tc.on, snap, commit)
			}
		}()
	}
}
