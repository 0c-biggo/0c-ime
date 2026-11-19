//go:build !windows

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #319：health 與 open 的 shift_tap_ms 是設定檔的「單按 Shift 的時限」（缺欄位或讀不懂時 300），
// 四個外殼照它判斷單按；以前固定回報 300。
func TestShiftTapSettingInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, tc := range []struct{ file, want string }{
		{`{"selection":"statistical"}`, "300"},
		{`{"selection":"statistical","shift_tap_ms":500}`, "500"},
		{`{"selection":"statistical","shift_tap_ms":200}`, "200"},
		{`{"selection":"statistical","shift_tap_ms":350}`, "300"},
	} {
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), []byte(tc.file), 0600); err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "tap.sock")
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["shift_tap_ms"] != tc.want {
				t.Fatalf("%s：health %q，要 %q", tc.file, health.Info["shift_tap_ms"], tc.want)
			}
			if open := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}); open.Info["shift_tap_ms"] != tc.want {
				t.Fatalf("%s：open %q，要 %q", tc.file, open.Info["shift_tap_ms"], tc.want)
			}
		}()
	}
}
