//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #258：settings.json 的 shift_symbol_halfwidth（缺欄位時是關）決定 Shift＋符號打半形；health 回報引擎載入的值，
// 設定程式據此比較「已儲存，尚未套用」；開著時 idle_keys 不列 ( 等鍵（沒在組字時交還應用程式）。
func TestShiftSymbolHalfwidthSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, tc := range []struct {
		file map[string]any
		on   bool
	}{{map[string]any{"selection": "statistical"}, false}, {map[string]any{"selection": "statistical", "shift_symbol_halfwidth": true}, true}} {
		b, _ := json.Marshal(tc.file)
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "shift.sock")
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
			health := call(ipc.Envelope{Op: ipc.OpHealth})
			if health.Info["shift_symbol_halfwidth"] != want || strings.Contains(health.Info["idle_keys"], "(") != !tc.on {
				t.Fatalf("health：%+v", health.Info)
			}
			open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
			req := proto.Request{Type: proto.Key, Key: "("}
			snap := call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &req}).Snapshot
			if snap == nil || snap.Handled == tc.on || (!tc.on && snap.Text() != "（") || (tc.on && snap.Text() != "") {
				t.Fatalf("shift_symbol_halfwidth=%v (：%+v", tc.on, snap)
			}
		}()
	}
}
