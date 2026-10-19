//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/internal/autoupdate"
)

var acceptanceEngine = flag.String("acceptance-installed-engine", "", "Optional installed engine for explicitly authorized isolated acceptance")

func TestMacInstallGuardRejectsBusyAndUnknownBeforeDrainingEitherEndpoint(t *testing.T) {
	for _, info := range []map[string]string{{"composing": "1"}, {}, {"composing": "-1"}, {"composing": "invalid"}} {
		drains := 0
		dial := func(time.Duration) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				var request ipc.Envelope
				if ipc.ReadFrame(server, &request) == nil {
					if request.Op == ipc.OpDrain {
						drains++
					}
					ipc.WriteFrame(server, ipc.Reply{OK: true, Info: info})
				}
			}()
			return client, nil
		}
		err := prepareMacInstall(context.Background(), []autoupdate.Dialer{modelStatusDial(map[string]string{"composing": "0", "pid": "2147483647"}, true), dial})
		if !errors.Is(err, autoupdate.ErrShellBusy) || drains != 0 {
			t.Fatalf("busy/unknown %v allowed handoff: %v drains=%d", info, err, drains)
		}
	}
}

func TestMacInstallGuardPreservesRealEngineCompositionAndSavesOnHandoff(t *testing.T) {
	dir := t.TempDir()
	exe, sock := filepath.Join(dir, "0c-server"), filepath.Join(dir, "engine.sock")
	build := exec.Command("go", "build", "-o", exe, "../0c-server")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build engine: %v %s", err, out)
	}
	if *acceptanceEngine != "" {
		exe = *acceptanceEngine
	}
	user := filepath.Join(dir, "user.json")
	process := exec.Command(exe, "-data", "../../testdata/toy", "-user", user, "-socket", sock, "-idle-exit", "0")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	t.Cleanup(func() { process.Process.Kill(); <-done })
	dial := func(timeout time.Duration) (net.Conn, error) { return net.DialTimeout("unix", sock, timeout) }
	var conn net.Conn
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		var err error
		conn, err = dial(time.Second)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("engine did not start")
	}
	defer conn.Close()
	call := func(request ipc.Envelope) ipc.Reply {
		t.Helper()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := ipc.WriteFrame(conn, request); err != nil {
			t.Fatal(err)
		}
		var reply ipc.Reply
		if err := ipc.ReadFrame(conn, &reply); err != nil || !reply.OK {
			t.Fatalf("reply: %+v %v", reply, err)
		}
		return reply
	}
	opened := call(ipc.Envelope{Op: ipc.OpOpen, Version: proto.Version})
	key := func(s string) ipc.Reply {
		return call(ipc.Envelope{Op: ipc.OpHandle, Session: opened.Session, Req: &proto.Request{Type: proto.Key, Key: s}})
	}
	for _, k := range "a86qi6" {
		key(string(k))
	}
	before := key(proto.KeyLeft).Snapshot
	composition := ""
	if before != nil {
		for _, segment := range before.Preedit {
			composition += segment.Text
		}
	}
	if composition != "麻婆" {
		t.Fatal("real Chinese composition positive control missing")
	}
	if err := prepareMacInstall(context.Background(), []autoupdate.Dialer{dial}); !errors.Is(err, autoupdate.ErrShellBusy) {
		t.Fatalf("real composition must block handoff: %v", err)
	}
	after := key(proto.KeyRight).Snapshot
	if after == nil || !reflect.DeepEqual(before.Preedit, after.Preedit) {
		t.Fatalf("composition lost: before=%+v after=%+v", before, after)
	}
	commit := key(proto.KeyEnter).Snapshot
	if commit == nil || commit.Commit != "麻婆" {
		t.Fatalf("preserved composition did not commit: %+v", commit)
	}
	// An explicit artificial tombstone gives a durable learning mutation to
	// verify; ordinary commits alone may legitimately leave learning empty.
	call(ipc.Envelope{Op: ipc.OpHandle, Session: opened.Session, Req: &proto.Request{Type: proto.Forget, Word: "麻婆豆腐"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := prepareMacInstall(ctx, []autoupdate.Dialer{dial}); err != nil {
		t.Fatalf("completed composition handoff failed: %v", err)
	}
	if data, err := os.ReadFile(user); err != nil || !bytes.Contains(data, []byte(`"麻婆豆腐":true`)) {
		t.Fatalf("artificial learning was not saved before endpoint release: %v", err)
	}
}

func TestMacInstallGuardDistinguishesAbsentSocketFromAccessFailure(t *testing.T) {
	for _, tc := range []struct {
		err     error
		allowed bool
	}{
		{syscall.ENOENT, true}, {syscall.ECONNREFUSED, true}, {syscall.EACCES, false}, {errors.New("timeout"), false},
	} {
		dial := func(time.Duration) (net.Conn, error) { return nil, tc.err }
		err := prepareMacInstall(context.Background(), []autoupdate.Dialer{dial})
		if (err == nil) != tc.allowed {
			t.Fatalf("%v allowed=%v got=%v", tc.err, tc.allowed, err)
		}
	}
}

func TestMacInstallGuardWaitsForEndpointReleaseAfterLostDrainReply(t *testing.T) {
	calls := 0
	dial := func(time.Duration) (net.Conn, error) {
		calls++
		if calls >= 4 {
			return nil, syscall.ENOENT
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var request ipc.Envelope
			if ipc.ReadFrame(server, &request) == nil && request.Op == ipc.OpHealth {
				ipc.WriteFrame(server, ipc.Reply{OK: true, Info: map[string]string{"composing": "0", "pid": "2147483647"}})
			}
		}()
		return client, nil
	}
	if err := prepareMacInstall(context.Background(), []autoupdate.Dialer{dial}); err != nil || calls != 4 {
		t.Fatalf("handoff must wait past lost reply until endpoint release: calls=%d err=%v", calls, err)
	}
}
