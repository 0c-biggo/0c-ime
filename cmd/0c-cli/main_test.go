package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFuturePersonalStoreContinuesInputWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "0c-cli")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	user := filepath.Join(dir, "user.json")
	original := []byte(`{"Format":99,"English":{"private":{"Text":"private","Confirmed":9}},"Future":{"value":1}}` + "\n")
	if err := os.WriteFile(user, original, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := filepath.Abs("../../testdata/toy")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-mixed-input", "-data", data, "-user", user)
	cmd.Stdin = bytes.NewBufferString("hello\n\n:q\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("hello")) || !bytes.Contains(out, []byte("較新版本")) {
		t.Fatalf("future store stopped input: %v\n%s", err, out)
	}
	got, err := os.ReadFile(user)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("future store overwritten: %v\n%s", err, got)
	}
}
