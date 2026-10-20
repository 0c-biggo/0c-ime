//go:build !windows

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/emoji"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// A palette choice in an unfinished composition must still mark the actual
// server writer dirty. Restarting must restore its ordering without learning
// the cancelled Chinese text or treating the emoji as an English word.
func TestPaletteOrderingSurvivesServerRestartWithoutCommit(t *testing.T) {
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	user, sock := filepath.Join(dir, "user.json"), filepath.Join(dir, "palette.sock")
	config := settings.Default()
	config.Selection, config.Punctuation = settings.Statistical, false
	config.CandidateLayout, config.EmojiShortcut = "grid", "equals"
	if err := settings.Save(filepath.Join(dir, settings.FileName), config); err != nil {
		t.Fatal(err)
	}
	for run := range 2 {
		server := exec.Command(binary, "-data", data, "-user", user, "-socket", sock)
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
				var reply ipc.Reply
				if err := ipc.ReadFrame(conn, &reply); err != nil {
					t.Fatal(err)
				}
				return reply
			}
			info := call(ipc.Envelope{Op: ipc.OpHealth}).Info
			if info["candidate_layout"] != "grid" || info["emoji_shortcut"] != "equals" || info["emoji_equals_first"] != "true" {
				t.Fatalf("saved settings not active: %+v", info)
			}
			id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
			handle := func(req proto.Request) *proto.Snapshot {
				t.Helper()
				snap := call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &req}).Snapshot
				if snap == nil {
					t.Fatal("missing snapshot")
				}
				return snap
			}
			for _, k := range "ji3" {
				handle(proto.Request{Type: proto.Key, Key: string(k)})
			}
			snap := handle(proto.Request{Type: proto.Key, Key: "="})
			if snap.Candidates == nil || snap.Candidates.Columns != 5 {
				t.Fatalf("missing grid: %+v", snap)
			}
			if run == 1 {
				if snap.Candidates.Items[0].Text != "=" || snap.Candidates.Items[0].ID != emoji.EqualsID || snap.Candidates.Items[1].Text != "😂" || snap.Candidates.Items[1].ID != emoji.ID("😂") {
					t.Fatal("recent ordering or stable selection ID lost on restart")
				}
				return
			}
			snap = handle(proto.Request{Type: proto.Select, Index: emoji.ID("😂")})
			if snap.Commit != "" || snap.Text() != "我😂" || !snap.PersonalChanged {
				t.Fatalf("choice did not remain in composition: %+v", snap)
			}
			handle(proto.Request{Type: proto.Cancel})
		}()
		file, err := os.Open(user)
		if err != nil {
			t.Fatal(err)
		}
		store, err := personal.LoadStore(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if store.Format != personal.StoreFormat || len(store.EmojiRecent) != 1 || store.EmojiRecent[0] != "😂" || store.PaletteTab != "emoji" || store.Total != 0 || len(store.English) != 0 {
			t.Fatalf("ordering not saved independently: %+v", store)
		}
	}
}
