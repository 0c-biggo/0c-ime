package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, map[string]int{"schema": 1}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\n  \"schema\": 1\n}\n" {
		t.Fatalf("%q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("暫存檔沒有清掉：%v", entries)
	}
}

func TestWriteFailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0o700); err != nil { // 目的地是非空目錄：改名失敗
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("x")); err == nil {
		t.Fatal("應失敗")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("失敗時暫存檔沒有清掉：%v", entries)
	}
	if _, err := os.Stat(filepath.Join(path, "keep")); err != nil {
		t.Fatal("原本的內容不應被動到")
	}
	if err := Write(filepath.Join(dir, "missing", "f"), nil); err == nil {
		t.Fatal("目錄不存在應失敗")
	}
}
