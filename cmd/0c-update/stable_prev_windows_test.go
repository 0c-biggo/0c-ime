//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Windows 上執行中的檔案不能覆寫、可以改名：穩定副本與上一版都正在執行（例如自動更新跑到一半，啟動器又以上一版執行
// 通知按鈕）時，換穩定副本仍要成功，執行中的那份成為上一版，被擠掉的舊上一版留到下次清掉。
func TestReplaceStableUpdaterWhileBothAreRunning(t *testing.T) {
	base := withPreviousKept(t)
	ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	b, err := os.ReadFile(ping)
	if err != nil {
		t.Skip(err)
	}
	stable, prev := stableUpdater(base), previousUpdater(base)
	writeFile(t, stable, string(b))
	writeFile(t, prev, string(append(append([]byte{}, b...), 0))) // 內容不同的另一份
	var running []*exec.Cmd
	for _, exe := range []string{stable, prev} {
		cmd := exec.Command(exe, "-n", "30", "127.0.0.1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		running = append(running, cmd)
	}
	defer func() {
		for _, cmd := range running {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	src := filepath.Join(base, "versions", "v4", updaterName())
	writeFile(t, src, "v4")
	if err := replaceStableUpdater(src, base); err != nil {
		t.Fatalf("replace while running: %v", err)
	}
	if contentOf(t, stable) != "v4" || contentOf(t, prev) != string(b) {
		t.Fatalf("stable %d bytes, prev %d bytes", len(contentOf(t, stable)), len(contentOf(t, prev)))
	}
	for _, cmd := range running {
		cmd.Process.Kill()
		cmd.Wait()
	}
	running = nil
	src5 := filepath.Join(base, "versions", "v5", updaterName())
	writeFile(t, src5, "v5")
	if err := replaceStableUpdater(src5, base); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(base, "bin"))
	if len(entries) != 2 || contentOf(t, stable) != "v5" || contentOf(t, prev) != "v4" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("after processes ended: %v", names)
	}
}
