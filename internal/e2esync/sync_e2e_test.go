//go:build e2etest

package e2esync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGateRequiresMatchingReleaseAndPreservesEvidence(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "e2e-sync")
	os.MkdirAll(dir, 0700)
	control := filepath.Join(dir, "writer_saved.json")
	os.WriteFile(control, []byte(`{"run":"case1","timeout_ms":2000}`), 0600)
	done := make(chan error, 1)
	go func() { done <- Pause(base, "writer_saved") }()
	ready := filepath.Join(dir, "writer_saved.case1.ready.json")
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ready evidence missing")
		}
		time.Sleep(time.Millisecond)
	}
	release := filepath.Join(dir, "writer_saved.case1.release")
	os.WriteFile(release, []byte("previous-run"), 0600)
	select {
	case err := <-done:
		t.Fatalf("wrong release opened gate: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	os.WriteFile(release, []byte("case1"), 0600)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(ready)
	os.WriteFile(control, []byte(`{"run":"case1","timeout_ms":2000}`), 0600)
	if err := Pause(base, "writer_saved"); err == nil {
		t.Fatal("must not overwrite previous evidence")
	}
	after, _ := os.ReadFile(ready)
	if string(before) != string(after) {
		t.Fatal("ready evidence changed")
	}
}

func TestGateTimesOutAndRejectsInvalidControls(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "e2e-sync")
	os.MkdirAll(dir, 0700)
	for _, cfg := range []string{`{"run":"../outside","timeout_ms":1}`, `{"run":"case","timeout_ms":5001}`, `{"run":"case","timeout_ms":20}`} {
		os.WriteFile(filepath.Join(dir, "core_safe.json"), []byte(cfg), 0600)
		if err := Pause(base, "core_safe"); err == nil {
			t.Fatal("invalid or unreleased control accepted")
		}
	}
}
