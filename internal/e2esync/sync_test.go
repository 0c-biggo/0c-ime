//go:build !e2etest

package e2esync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProductionIgnoresSynchronizationControls(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "e2e-sync")
	os.MkdirAll(dir, 0700)
	control := filepath.Join(dir, "core_safe.json")
	os.WriteFile(control, []byte(`{"run":"case","timeout_ms":5000}`), 0600)
	if err := Pause(base, "core_safe"); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "core_safe.json" {
		t.Fatalf("production accessed test controls: %v %v", files, err)
	}
}
