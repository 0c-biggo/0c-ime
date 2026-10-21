package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// #179：排程每次喚起（auto）補做清理，接手沒做完的背景清理；登入時、選單或設定程式等著結果的呼叫（-json、-notify）不做。
func TestAutoPrunesOnlyFromSchedule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".local/share/zuin")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := editState(base, func(s *autoupdate.State) { s.Mode = autoupdate.ModeAuto; s.Feed = "https://127.0.0.1:1/feed.json" }); err != nil {
		t.Fatal(err)
	}
	var pruned []string
	saved := pruneInstalls
	pruneInstalls = func(b string) { pruned = append(pruned, b) }
	t.Cleanup(func() { pruneInstalls = saved; pruneFrom = "" })

	pruneFrom = "/somewhere/0c-update" // 這次喚起裝了新版：已在同一個行程清過，不再另外啟動
	_ = autoCommand("auto", []string{"-now"})
	if len(pruned) != 1 || pruned[0] != base {
		t.Fatalf("auto pruned %v, want once in %s", pruned, base)
	}
	if pruneFrom != "" {
		t.Fatal("auto also starts a background prune after pruning itself")
	}
	for _, args := range [][]string{{"-login"}, {"-now", "-json"}} {
		_ = autoCommand("auto", args)
	}
	if len(pruned) != 1 {
		t.Fatalf("pruned outside the scheduled run: %v", pruned)
	}
}
