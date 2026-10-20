//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// #179：背景清理行程沒有主控台、自成行程群組、低優先順序，工作目錄在安裝目錄（不占住安裝程式的暫存目錄），
// 不接呼叫端的輸出管線；啟動後不等它。
func TestSpawnPruneIsDetached(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := os.MkdirAll(basePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := pruneProcess(`C:\x\0c-update.exe`, basePath(), pruneCreationFlags)
	if len(cmd.Args) != 2 || cmd.Args[1] != "prune" || cmd.Dir != basePath() || cmd.Stdout != nil || cmd.Stderr != nil || cmd.Stdin != nil {
		t.Fatalf("prune process = %+v", cmd)
	}
	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.DETACHED_PROCESS == 0 || flags&windows.CREATE_NEW_PROCESS_GROUP == 0 || flags&windows.BELOW_NORMAL_PRIORITY_CLASS == 0 {
		t.Fatalf("creation flags = %#x", flags)
	}
	// 真的啟動一次（以系統的 whoami 代替 0c-update；它不認得 prune，馬上結束）：不等它結束就回傳。
	start := time.Now()
	if err := spawnPrune(filepath.Join(os.Getenv("SystemRoot"), `System32\whoami.exe`)); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("spawnPrune waited for the process")
	}
	if err := spawnPrune(filepath.Join(t.TempDir(), "missing.exe")); err == nil {
		t.Fatal("no error for a missing program")
	}
}

// 回復、解除安裝從穩定副本執行時用自己；從別處（安裝程式的暫存目錄）執行時用收據版本目錄裡的那一份。
func TestPruneExecutableFallsBackToReceipt(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := os.MkdirAll(basePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath(), []byte(`{"version":"0.2.3","directory":"versions/0.2.3-0123456789ab","active":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(basePath(), "versions", "0.2.3-0123456789ab", "0c-update.exe")
	if got := pruneExecutable(); got != want { // 測試程式在 Go 的暫存目錄，不在安裝目錄裡
		t.Fatalf("pruneExecutable() = %q, want %q", got, want)
	}
}

// 背景清理行程：沒有安裝時不做事；安裝鎖被占用時放棄（不算失敗），交給下一次。
func TestPruneCommandGivesUpWhenLocked(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if err := pruneOnce(basePath()); err != nil {
		t.Fatalf("no installation: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(basePath(), "versions", "0.1.0-dev.0-0123456789ab"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath(), []byte(`{"schema":1,"product":"0c","version":"0.2.3","directory":"versions/0.2.3-0123456789ab","active":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(basePath(), "update.lock"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneOnce(basePath()); err != nil {
		t.Fatalf("locked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(basePath(), "versions", "0.1.0-dev.0-0123456789ab")); err != nil {
		t.Fatal("pruned while locked")
	}
}
