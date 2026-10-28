package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #238：0.2.6 的 0c --activate-input-source 在登入後的排程裡 40 秒還沒結束；更新器呼叫它要有上限，
// 工具卡住（例如等系統設定回應）時照樣結束、回報失敗，由呼叫端記下登入後補做。
func TestActivateInputSourceGivesUpOnAHungTool(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "0c")
	// 工具與它留下的子行程都拿著輸出不放，模擬卡在系統呼叫裡的 0c。
	script := "#!/bin/sh\necho 已登記\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := activateTimeout
	activateTimeout = time.Second
	defer func() { activateTimeout = saved }()
	start := time.Now()
	err := activateInputSource(tool, filepath.Join(dir, "0c.app"))
	if err == nil {
		t.Fatal("a hung tool must be reported as a failure")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("activateInputSource waited %v for a hung tool", elapsed)
	}
	if !strings.Contains(err.Error(), "仍未結束") {
		t.Fatalf("error should say the tool timed out: %v", err)
	}
}

func TestActivateInputSourcePassesForceAndReportsExitStatus(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "0c")
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + args + "\necho 尚未啟用 >&2\nexit 2\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := activateInputSource(tool, "/x/0c.app", true)
	if err == nil || !strings.Contains(err.Error(), "尚未啟用") || !strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("exit status 2 must be reported with the tool output: %v", err)
	}
	b, _ := os.ReadFile(args)
	if got := strings.TrimSpace(string(b)); got != "--activate-input-source /x/0c.app --force" {
		t.Fatalf("args = %q", got)
	}
}
