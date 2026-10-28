//go:build !windows

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/internal/englishpack"
	"github.com/0c-biggo/0c-ime/internal/settings"
)

// Restart the isolated product server with the saved on/off setting. This checks
// actual startup wiring, rather than just assigning a bool on a test session.
func TestSavedEnglishAutocompleteSurvivesServerRestart(t *testing.T) {
	testSavedEnglishAutocomplete(t, false)
}

func TestFullEnglishAutocompleteSurvivesServerRestart(t *testing.T) {
	testSavedEnglishAutocomplete(t, true)
}

func testSavedEnglishAutocomplete(t *testing.T, full bool) {
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
	sock := filepath.Join(dir, "autocomplete.sock")
	config := settings.Default()
	config.Selection = settings.Statistical
	config.Punctuation = false
	for _, enabled := range []bool{true, false, true} {
		config.EnglishAutocomplete = enabled
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
			if health := call(ipc.Envelope{Op: ipc.OpHealth}); health.Info["english_autocomplete"] != strconv.FormatBool(enabled) || health.Info["english_words"] != strconv.Itoa(words) {
				t.Fatalf("wrong runtime toggle: %+v", health.Info)
			}
			id := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version}).Session
			call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &proto.Request{Type: proto.Mode, On: true}})
			for _, k := range "hel" {
				call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &proto.Request{Type: proto.Key, Key: string(k)}})
			}
			out := call(ipc.Envelope{Op: ipc.OpHandle, Session: id, Req: &proto.Request{Type: proto.Key, Key: proto.KeyEnter}})
			if out.Snapshot == nil || out.Snapshot.Handled != enabled || (out.Snapshot.Commit == "hel") != enabled {
				t.Fatalf("autocomplete=%v: %+v", enabled, out.Snapshot)
			}
		}()
	}
}
