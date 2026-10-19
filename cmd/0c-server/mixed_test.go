//go:build !windows

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/internal/englishpack"
	"git.biggo.com/0c/ime/internal/settings"
)

// Restart the isolated product server with the saved on/off setting. This checks
// actual startup wiring, rather than just assigning a bool on a test session.
func TestSavedMixedToggleSurvivesServerRestart(t *testing.T) {
	testSavedMixedToggle(t, false)
}

func TestFullEnglishPackSavedToggleSurvivesServerRestart(t *testing.T) {
	testSavedMixedToggle(t, true)
}

func testSavedMixedToggle(t *testing.T, full bool) {
	t.Helper()
	binary := buildServer(t)
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	words := 104
	if full {
		target := filepath.Join(dir, "data")
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"lexicon.tsv", "lm.tsv"} {
			b, err := os.ReadFile(filepath.Join(data, name))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(target, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := englishpack.Compile("../../data/english", filepath.Join(target, "english")); err != nil {
			t.Fatal(err)
		}
		data, words = target, 82015
	}
	sock := filepath.Join(dir, "mixed.sock")
	config := settings.Default()
	config.Selection = settings.Statistical
	config.Punctuation = false
	for _, enabled := range []bool{true, false, true} {
		config.MixedInput = enabled
		if err := settings.Save(filepath.Join(dir, settings.FileName), config); err != nil {
			t.Fatal(err)
		}
		server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", sock)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		stop := func() { server.Process.Signal(os.Interrupt); server.Wait() }
		func() {
			defer stop()
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["mixed_input"] != strconv.FormatBool(enabled) || health.Info["english_words"] != strconv.Itoa(words) {
				t.Fatalf("wrong runtime toggle: %+v", health.Info)
			}
			id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
			for _, k := range "hello" {
				call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
			}
			out := call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &proto.Request{Type: proto.Commit}})
			if out.Snapshot == nil || (out.Snapshot.Commit == "hello") != enabled {
				t.Fatalf("mixed=%v: %+v", enabled, out.Snapshot)
			}
		}()
	}
}
