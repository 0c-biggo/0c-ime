package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func oldCopies(t *testing.T, dir, name string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), name+".old") {
			found = append(found, e.Name())
		}
	}
	return found
}

func TestReplaceUpdaterLeavesNoOldCopiesBehind(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "0c-update")
	src := filepath.Join(dir, "new")
	if err := os.WriteFile(dst, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	// 先前替換留下的舊檔（固定名字與帶編號的）在下一次替換時清掉。
	for _, name := range []string{"0c-update.old", "0c-update.old-123"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("leftover"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := replaceUpdater(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("destination is %q", b)
	}
	if left := oldCopies(t, dir, "0c-update"); len(left) != 0 {
		t.Fatalf("old copies left behind: %v", left)
	}
}

// Windows 上仍在執行的舊映像刪不掉（上一次替換留下的 .old 可能正被一個跑很久的 auto 使用）。
// 這裡以「刪不掉也佔著名字」的非空目錄代替：固定名字的 .old 會讓下一次替換在改名時失敗。
func TestReplaceUpdaterSucceedsWhenAnEarlierOldCopyCannotBeRemoved(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "0c-update")
	src := filepath.Join(dir, "new")
	if err := os.WriteFile(dst, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(dir, "0c-update.old")
	if err := os.MkdirAll(filepath.Join(stuck, "in-use"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := replaceUpdater(src, dst); err != nil {
		t.Fatalf("replace failed because of an old copy that cannot be removed: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("destination is %q", b)
	}
	if left := oldCopies(t, dir, "0c-update"); len(left) != 1 || left[0] != "0c-update.old" {
		t.Fatalf("only the one that cannot be removed may remain, got %v", left)
	}
}
