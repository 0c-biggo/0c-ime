//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/settings"
)

// #118：settings.json 的 output_simplified 開啟時，經 IPC 送出的文字是簡體，組字區仍是繁體；
// health 回報引擎實際載入的值，給設定程式比較「已儲存，尚未套用」。
func TestOutputSimplifiedSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, on := range []bool{false, true} {
		b, _ := json.Marshal(map[string]any{"selection": "statistical", "output_simplified": on})
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "simplified.sock")
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
			want := map[bool]string{false: "false", true: "true"}[on]
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["output_simplified"] != want {
				t.Fatalf("health：%+v", health.Info)
			}
			id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
			handle := func(req proto.Request) *proto.Snapshot {
				return call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &req}).Snapshot
			}
			var snap *proto.Snapshot
			for _, y := range []string{"ㄈㄥ", "ㄐㄧㄥˇ"} {
				for _, k := range layout.Standard.Keys(zhuyin.Syllable(y)) {
					snap = handle(proto.Request{Type: proto.Key, Key: string(k)})
				}
			}
			if snap == nil || snap.Text() != "風景" {
				t.Fatalf("組字區應是繁體：%+v", snap)
			}
			commit := map[bool]string{false: "風景", true: "风景"}[on]
			if snap = handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap == nil || snap.Commit != commit {
				t.Fatalf("output_simplified=%v：送出 %+v，預期 %q", on, snap, commit)
			}
		}()
	}
}
