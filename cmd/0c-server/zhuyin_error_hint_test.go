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

// #300：settings.json 的 zhuyin_error_hint（缺欄位時是開）決定按了聲調但讀音沒有字時要不要擋下聲調、要外殼提示；
// health 回報引擎載入的值，設定程式據此比較「已儲存，尚未套用」。
func TestZhuyinErrorHintSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, tc := range []struct {
		file map[string]any
		on   bool
	}{{map[string]any{"selection": "statistical"}, true}, {map[string]any{"selection": "statistical", "zhuyin_error_hint": false}, false}} {
		b, _ := json.Marshal(tc.file)
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "hint.sock")
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["zhuyin_error_hint"] != want {
				t.Fatalf("health：%+v", health.Info)
			}
			open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
			do := func(req proto.Request) *proto.Snapshot {
				return call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &req}).Snapshot
			}
			var snap *proto.Snapshot
			for _, k := range []string{"1", "m", "6"} { // ㄅㄩˊ：沒有這個讀音
				snap = do(proto.Request{Type: proto.Key, Key: k})
			}
			if text := map[bool]string{false: "ㄅㄩˊ", true: "ㄅㄩ"}[tc.on]; snap == nil || snap.Text() != text || snap.Bell != tc.on {
				t.Fatalf("zhuyin_error_hint=%v：%+v，組字要是 %q", tc.on, snap, text)
			}
			if tc.on {
				if snap = do(proto.Request{Type: proto.Key, Key: "6"}); snap == nil || snap.Text() != "ㄅㄩˊ" || snap.Bell {
					t.Fatalf("緊接著再按 ˊ 原樣留下：%+v", snap)
				}
			}
		}()
	}
}
