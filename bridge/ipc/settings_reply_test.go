package ipc

import (
	"github.com/0c-biggo/0c-ime/core/proto"
	"net"
	"testing"
	"time"
)

func TestDrainDeliversFinalCommitReplyBeforeDisconnect(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	drained := make(chan struct{})
	srv := &Server{Engine: toyEngine(t), conns: map[net.Conn]bool{server: true}, OnDrained: func() { close(drained) }}
	go srv.handle(server)
	call := func(e Envelope) Reply {
		t.Helper()
		if err := WriteFrame(client, e); err != nil {
			t.Fatal(err)
		}
		var r Reply
		if err := ReadFrame(client, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	open := call(Envelope{Op: OpOpen, Version: proto.Version})
	r := call(Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Key, Key: "a"}})
	if r.Snapshot == nil || r.Snapshot.Text() == "" {
		t.Fatalf("fixture must compose before commit: %+v", r.Snapshot)
	}
	if err := WriteFrame(client, Envelope{Op: OpHandle, Session: open.Session, Req: &proto.Request{Type: proto.Commit}}); err != nil {
		t.Fatal(err)
	}
	// The core has consumed the composition, but net.Pipe cannot finish the reply
	// before the client reads it. Persistent settings drain must await that write.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, n := srv.Engine.Sessions()
		if n == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !srv.Drain() {
		t.Fatal("drain did not start")
	}
	select {
	case <-drained:
		t.Error("drained before the commit reply was delivered")
	case <-time.After(100 * time.Millisecond):
	}
	var reply Reply
	if err := ReadFrame(client, &reply); err != nil {
		t.Fatalf("commit reply lost: %v", err)
	}
	if reply.Snapshot == nil || reply.Snapshot.Commit != r.Snapshot.Text() {
		t.Fatalf("wrong final commit: %+v", reply.Snapshot)
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after reply delivery")
	}
}
