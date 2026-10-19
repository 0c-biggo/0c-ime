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
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/internal/settings"
)

func TestSavedCandidateCursorAndPlatformDefaultInServer(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	for _, direction := range []string{"", "before", "after", "before"} {
		config := map[string]any{"selection": "statistical", "punctuation": false}
		if direction != "" {
			config["candidate_cursor"] = direction
		}
		b, _ := json.Marshal(config)
		if err := os.WriteFile(filepath.Join(dir, settings.FileName), b, 0600); err != nil {
			t.Fatal(err)
		}
		want := direction
		if want == "" {
			want = settings.Default().CandidateCursor
		}
		sock := filepath.Join(dir, "cursor.sock")
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["candidate_cursor"] != want {
				t.Fatalf("%+v", health.Info)
			}
			id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
			handle := func(req proto.Request) *proto.Snapshot {
				return call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &req}).Snapshot
			}
			for _, k := range []string{"j", "i", "3", "t", " "} {
				handle(proto.Request{Type: proto.Key, Key: k})
			}
			handle(proto.Request{Type: proto.Key, Key: proto.KeyLeft})
			snap := handle(proto.Request{Type: proto.Key, Key: proto.KeyDown})
			start := 1
			if want == "before" {
				start = 0
			}
			if snap == nil || snap.Candidates == nil || snap.Candidates.Start != start || snap.Candidates.Length != 1 || snap.Cursor.Rune != 1 {
				t.Fatalf("direction=%s: %+v", want, snap)
			}
			handle(proto.Request{Type: proto.Cancel}) // no user vocabulary written by this test
		}()
	}
}
