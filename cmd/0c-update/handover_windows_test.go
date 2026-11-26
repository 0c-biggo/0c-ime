//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

// 一般權限時先以一般權限交給新版；新版說要改註冊（needs_consent）而且使用者親自要求安裝時，以 UAC 提權的是同一個
// 新版（帶原使用者與安裝目錄），使用者取消 UAC 時待辦保留並照實回報（結束碼 75）。背景排程不提權。
func TestHandoverElevatesTheNewUpdaterOnlyWhenItAsks(t *testing.T) {
	if elevatedOK() {
		t.Skip("以系統管理員執行時直接執行新版，不經 UAC")
	}
	var file string
	var args []string
	launches := 0
	old := launchHandoverElevated
	launchHandoverElevated = func(f string, a []string) (windows.Handle, error) {
		launches++
		file, args = f, a
		return 0, windows.ERROR_CANCELLED
	}
	t.Cleanup(func() { launchHandoverElevated = old })
	exe := os.Args[0]
	req := handoverRequest{Root: `C:\stage\0c`, Version: "0.2.16", Result: filepath.Join(t.TempDir(), "r.json")}

	t.Setenv(handoverChildEnv, "raw:needs_consent")
	if err := runHandover(context.Background(), exe, req); err == nil || launches != 0 {
		t.Fatalf("background run must not elevate: %v launches=%d", err, launches)
	}
	if out, err := readHandoverResult(req.Result); err != nil || out.Reason != "needs_consent" {
		t.Fatalf("%+v %v", out, err)
	}
	req.Consent = true
	err := runHandover(context.Background(), exe, req)
	if !errors.Is(err, errHandoverCancelled) || !userCancelled(err) || launches != 1 {
		t.Fatalf("%v launches=%d", err, launches)
	}
	sid, _ := processUserSID()
	if file != exe || !slices.Contains(args, "-origin-user") || !slices.Contains(args, sid) || !slices.Contains(args, basePath()) || !slices.Contains(args, "-log") || !slices.Contains(args, "-consent") {
		t.Fatalf("%s %q", file, args)
	}
	// 新版以一般權限裝好（註冊不變）：不提權。
	t.Setenv(handoverChildEnv, "raw:")
	launches = 0
	_ = runHandover(context.Background(), exe, req)
	if launches != 0 {
		t.Fatal("elevated although the new updater did not ask")
	}
}
