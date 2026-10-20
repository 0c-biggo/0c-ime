package engine

import (
	"encoding/json"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

func TestMixedToggleAndByteProtocol(t *testing.T) {
	e := testEngine()
	if e.Session(e.OpenSession()).MixedInput {
		t.Fatal("default must preserve manual switching")
	}
	e.MixedInput = true
	id := e.OpenSession()
	e.Do(id, proto.Request{Type: proto.Key, Key: "h"})
	if _, composing := e.Sessions(); composing != 1 {
		t.Fatal("pending English must block engine handoff")
	}
	e.Do(id, proto.Request{Type: proto.Cancel})
	for _, k := range "hellot " {
		if _, err := e.Do(id, proto.Request{Type: proto.Key, Key: string(k)}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.Handle(id, []byte(`{"type":"commit"}`))
	if err != nil {
		t.Fatal(err)
	}
	var snap proto.Snapshot
	if err := json.Unmarshal(out, &snap); err != nil || snap.Commit != "hello吃" {
		t.Fatalf("%s %v", out, err)
	}
	e.MixedInput = false
	next := e.OpenSession()
	if e.Session(next).MixedInput || !e.Session(id).MixedInput {
		t.Fatal("new sessions must get configured toggle")
	}
}
