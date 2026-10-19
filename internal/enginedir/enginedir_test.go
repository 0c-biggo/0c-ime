package enginedir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPointerRoundTripAndValidation(t *testing.T) {
	base := t.TempDir()
	if c, err := Read(base); c != nil || err != nil {
		t.Fatal("missing pointer must read as none")
	}
	if _, _, ok := Resolve(base); ok {
		t.Fatal("no pointer must not resolve")
	}
	dir := filepath.Join(base, "versions", "0.1.0-dev.18-abc")
	os.MkdirAll(filepath.Join(dir, "data"), 0o700)
	if err := Write(base, Current{Directory: "versions/0.1.0-dev.18-abc", Version: "0.1.0-dev.18"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Resolve(base); ok {
		t.Fatal("incomplete version directory must not resolve")
	}
	os.WriteFile(filepath.Join(dir, Executable()), []byte("x"), 0o700)
	os.WriteFile(filepath.Join(dir, "data", "manifest.json"), []byte("{}"), 0o600)
	exe, data, ok := Resolve(base)
	if !ok || exe != filepath.Join(dir, Executable()) || data != filepath.Join(dir, "data") {
		t.Fatalf("resolve: %s %s %v", exe, data, ok)
	}
	for _, bad := range []string{"../x", "versions/../x", "versions/a/b", "other/x", `versions\x`, "versions/", "versions/."} {
		if err := Write(base, Current{Directory: bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	os.WriteFile(filepath.Join(base, FileName), []byte(`{"schema":1,"directory":"../../etc"}`), 0o600)
	if _, err := Read(base); err == nil {
		t.Fatal("tampered pointer accepted")
	}
	if err := Remove(base); err != nil || Remove(base) != nil {
		t.Fatal("remove must be idempotent")
	}
}
