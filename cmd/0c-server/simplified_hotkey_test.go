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

// #192：settings.json 的 simplified_hotkey（缺欄位時預設關）經 health 與 open 的 info 告訴外殼要不要攔切換鍵；
// 按下時外殼送的是選單同一個 output_simplified 請求，引擎即時切換並寫回設定檔，快捷鍵開關保持原值。
func TestSimplifiedHotkeySettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	path := filepath.Join(dir, settings.FileName)
	for _, tc := range []struct {
		file map[string]any
		on   bool
	}{{map[string]any{"selection": "statistical"}, false}, {map[string]any{"selection": "statistical", "simplified_hotkey": true}, true}} {
		b, _ := json.Marshal(tc.file)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "hotkey.sock")
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["simplified_hotkey"] != want || health.Info["output_simplified_live"] != "true" {
				t.Fatalf("health：%+v", health.Info)
			}
			open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
			if open.Info["simplified_hotkey"] != want {
				t.Fatalf("open：%+v", open.Info)
			}
			if !tc.on {
				return
			}
			req := proto.Request{Type: proto.OutputSimplified, On: true}
			snap := call(ipc.Envelope{Op: ipc.OpHandle, Session: open.Session, Req: &req}).Snapshot
			if snap == nil || !snap.Handled || !snap.OutputSimplified {
				t.Fatalf("切換：%+v", snap)
			}
			saved, err := settings.Load(path)
			if err != nil || !saved.OutputSimplified || !saved.SimplifiedHotkey {
				t.Fatalf("寫回設定檔要保留快捷鍵開關：%+v %v", saved, err)
			}
		}()
	}
}
