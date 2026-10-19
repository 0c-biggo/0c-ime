package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.biggo.com/0c/ime/internal/enginedir"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// 安裝後第一次建立、之後版本不同就換、相同就不動（#126）。
func TestSyncStableMCPCreatesThenFollowsTheCurrentVersion(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "bin", mcpName())
	v1 := filepath.Join(root, "versions", "a", mcpName())
	v2 := filepath.Join(root, "versions", "b", mcpName())
	writeFile(t, v1, "mcp 1")
	writeFile(t, v2, "mcp 2")
	for _, step := range []struct {
		source  string
		changed bool
		want    string
	}{{v1, true, "mcp 1"}, {v1, false, "mcp 1"}, {v2, true, "mcp 2"}} {
		changed, err := syncStableMCP(step.source, hashOf(t, step.source), stable)
		if err != nil || changed != step.changed {
			t.Fatalf("%s: changed=%v err=%v", step.source, changed, err)
		}
		if got := read(t, stable); got != step.want {
			t.Fatalf("stable copy is %q, want %q", got, step.want)
		}
	}
}

// 來源與安裝紀錄不符（被改過、不完整）時不換。
func TestSyncStableMCPRefusesAnUnverifiedSource(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "bin", mcpName())
	source := filepath.Join(root, "versions", "a", mcpName())
	writeFile(t, source, "tampered")
	writeFile(t, stable, "old")
	if changed, err := syncStableMCP(source, strings.Repeat("0", 64), stable); err == nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if got := read(t, stable); got != "old" {
		t.Fatalf("stable copy must be kept: %q", got)
	}
}

// 目前版本沒有附 0c-mcp（回復到舊版、解除安裝）時移走穩定副本，設定程式才會照實註明；本來就沒有時不算變更。
func TestSyncStableMCPRetiresTheCopyWhenTheCurrentVersionHasNone(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "bin", mcpName())
	writeFile(t, stable, "mcp 1")
	writeFile(t, stable+".old-1", "leftover")
	if changed, err := syncStableMCP("", "", stable); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Lstat(stable); !os.IsNotExist(err) {
		t.Fatalf("stable copy must be gone: %v", err)
	}
	if _, err := os.Lstat(stable + ".old-1"); !os.IsNotExist(err) {
		t.Fatalf("leftover .old copies are cleaned: %v", err)
	}
	if changed, err := syncStableMCP("", "", stable); err != nil || changed {
		t.Fatalf("nothing to retire: changed=%v err=%v", changed, err)
	}
	if err := os.MkdirAll(stable, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := syncStableMCP("", "", stable); err == nil {
		t.Fatal("a directory in place of the copy must not be removed")
	}
}

// 目前版本：核心目錄記錄了 0c-mcp 就用它；沒有記錄（舊更新器準備的核心目錄）時退回完整安裝收據，
// 已停用的收據（解除安裝）則沒有。
func TestCurrentMCPFollowsTheCoreThenTheReceipt(t *testing.T) {
	base := t.TempDir()
	receipt := filepath.Join(base, "receipt.json")
	name := mcpName()
	writeReceipt := func(files map[string]string, active bool) {
		b, _ := json.Marshal(map[string]any{"directory": "versions/0.2.0-abc", "active": active, "files": files})
		if err := os.WriteFile(receipt, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeReceipt(map[string]string{updaterName(): "aa"}, true)
	if p, h := currentMCP(base, receipt); p != "" || h != "" {
		t.Fatalf("a version without 0c-mcp: %q %q", p, h)
	}
	writeReceipt(map[string]string{updaterName(): "aa", name: "cc"}, true)
	if p, h := currentMCP(base, receipt); p != filepath.Join(base, "versions", "0.2.0-abc", name) || h != "cc" {
		t.Fatalf("receipt: %q %q", p, h)
	}
	core := filepath.Join(base, "versions", "0.2.1-c-123")
	if err := os.MkdirAll(core, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := enginedir.Write(base, enginedir.Current{Directory: "versions/0.2.1-c-123", Version: "0.2.1"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := currentMCP(base, receipt); p != filepath.Join(base, "versions", "0.2.0-abc", name) {
		t.Fatalf("a core without 0c-mcp falls back to the receipt: %q", p)
	}
	if err := enginedir.WriteFiles(core, map[string]string{name: "dd"}); err != nil {
		t.Fatal(err)
	}
	if p, h := currentMCP(base, receipt); p != filepath.Join(core, name) || h != "dd" {
		t.Fatalf("core: %q %q", p, h)
	}
	if err := enginedir.Remove(base); err != nil {
		t.Fatal(err)
	}
	writeReceipt(map[string]string{name: "cc"}, false)
	if p, _ := currentMCP(base, receipt); p != "" {
		t.Fatalf("an uninstalled receipt has no current 0c-mcp: %q", p)
	}
}
