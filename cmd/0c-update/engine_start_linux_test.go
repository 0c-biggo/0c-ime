//go:build !windows && !darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// 交接時的引擎交給 systemd user manager 以暫時性 service 啟動：由 manager 產生行程，不繼承
// 0c-update.service 的 Nice=10 與 idle I/O（#141）。不能用 --scope（scope 由呼叫者產生，會繼承）。
func TestEngineUnitArgs(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "XDG_RUNTIME_DIR": "/run/user/1000"}
	now := time.Unix(1760000000, 123)
	args := engineUnitArgs("/home/u/.local/bin/0c-server", now, func(k string) string { return env[k] })
	for _, want := range []string{"--user", "--collect", "--quiet", "--setenv=HOME=/home/u", "--setenv=XDG_RUNTIME_DIR=/run/user/1000"} {
		if !slices.Contains(args, want) {
			t.Fatalf("缺少 %s：%q", want, args)
		}
	}
	for _, bad := range []string{"--scope", "--wait", "--no-block", "--pty"} {
		if slices.Contains(args, bad) {
			t.Fatalf("不該有 %s：%q", bad, args)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "Nice") || strings.Contains(a, "IOScheduling") {
			t.Fatalf("不指定優先權，沿用 manager 的一般優先權：%q", args)
		}
	}
	unit := ""
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--unit="); ok {
			unit = v
		}
	}
	// 每次名稱不同：舊引擎可能還在存檔、所屬單元還沒卸載，同名會被 systemd 拒絕。
	if unit != "0c-server-1760000000000000123" {
		t.Fatalf("unit = %q", unit)
	}
	if other := engineUnitArgs("/x", now.Add(time.Nanosecond), func(k string) string { return env[k] }); slices.Contains(other, "--unit="+unit) {
		t.Fatalf("不同時間產生相同單元名稱：%q", other)
	}
	if n := len(args); n < 2 || args[n-2] != "--" || args[n-1] != "/home/u/.local/bin/0c-server" {
		t.Fatalf("引擎路徑要在 -- 之後、不帶參數（由 engine.json 轉交版本目錄）：%q", args)
	}

	// 呼叫端沒有的變數不傳空值，交給 manager 的預設。
	args = engineUnitArgs("/x", now, func(string) string { return "" })
	for _, a := range args {
		if strings.HasPrefix(a, "--setenv=") {
			t.Fatalf("空變數不應傳入：%q", args)
		}
	}
}

type engineStartFake struct {
	lookErr  error
	runErr   error
	spawnErr error
	ran      [][]string
	spawned  []string
}

func (f *engineStartFake) install(t *testing.T) {
	t.Helper()
	oldLook, oldRun, oldSpawn := lookSystemdRun, runSystemdRun, spawnEngineDirect
	t.Cleanup(func() { lookSystemdRun, runSystemdRun, spawnEngineDirect = oldLook, oldRun, oldSpawn })
	lookSystemdRun = func() (string, error) {
		if f.lookErr != nil {
			return "", f.lookErr
		}
		return "/usr/bin/systemd-run", nil
	}
	runSystemdRun = func(_ context.Context, path string, args []string) ([]byte, error) {
		f.ran = append(f.ran, append([]string{path}, args...))
		if f.runErr != nil {
			return []byte("Failed to connect to bus: No medium found\n"), f.runErr
		}
		return nil, nil
	}
	spawnEngineDirect = func(server string) error {
		f.spawned = append(f.spawned, server)
		return f.spawnErr
	}
}

// fakeStableServer 在暫時 HOME 放一個穩定入口。
func fakeStableServer(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := filepath.Join(home, ".local/bin/0c-server")
	if err := os.MkdirAll(filepath.Dir(server), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(server, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return server
}

func TestStartEnginePrefersSystemdRun(t *testing.T) {
	server := fakeStableServer(t)

	f := &engineStartFake{}
	f.install(t)
	if err := startEngine(""); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || f.ran[0][0] != "/usr/bin/systemd-run" || f.ran[0][len(f.ran[0])-1] != server {
		t.Fatalf("systemd-run 呼叫 = %q", f.ran)
	}
	if len(f.spawned) != 0 {
		t.Fatalf("systemd-run 成功時不該再直接啟動：%q", f.spawned)
	}
}

// 沒有 systemd、沒有 user bus（ssh、su、容器）或單元建立失敗時，退回直接啟動。
func TestStartEngineFallsBackToDirectSpawn(t *testing.T) {
	server := fakeStableServer(t)

	for name, f := range map[string]*engineStartFake{
		"no systemd-run": {lookErr: errors.New("not found")},
		"no user bus":    {runErr: errors.New("exit status 1")},
	} {
		f.install(t)
		if err := startEngine(""); err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		if len(f.spawned) != 1 || f.spawned[0] != server {
			t.Fatalf("%s：應退回直接啟動 %s，實際 %q", name, server, f.spawned)
		}
	}

	f := &engineStartFake{runErr: errors.New("exit status 1"), spawnErr: errors.New("exec failed")}
	f.install(t)
	if err := startEngine(""); err == nil || !strings.Contains(err.Error(), "exec failed") {
		t.Fatalf("兩種方式都失敗時要回報直接啟動的錯誤：%v", err)
	}

	// 穩定入口不存在時不經 systemd：直接回報錯誤，不留下一個啟動即失敗的單元、等 15 秒逾時。
	t.Setenv("HOME", t.TempDir())
	f = &engineStartFake{spawnErr: errors.New("no such file")}
	f.install(t)
	if err := startEngine(""); err == nil || len(f.ran) != 0 {
		t.Fatalf("穩定入口不存在：err=%v systemd-run=%q", err, f.ran)
	}
}

// 更新後重新啟動的 Fcitx5 也交給 user manager：不帶 0c-update.service 的 Nice=10／idle I/O，之後它啟動的引擎也不帶。
// 前景執行（不加 -d），KillMode=process 讓 Fcitx5 結束時不收掉它啟動的引擎；環境逐一帶上。
func TestFcitxUnitArgs(t *testing.T) {
	now := time.Unix(1760000000, 7)
	env := []string{"DISPLAY=:0", "PATH=/usr/bin", "BAD-NAME=x", "MULTI=a\nb", "DISPLAY=:1", "FCITX_ADDON_DIRS=/home/u/.local/lib/fcitx5"}
	args := fcitxUnitArgs([]string{"--disable=wayland"}, env, now)
	for _, want := range []string{"--user", "--collect", "--property=KillMode=process", "--setenv=DISPLAY=:1", "--setenv=PATH=/usr/bin",
		"--setenv=FCITX_ADDON_DIRS=/home/u/.local/lib/fcitx5", "--unit=0c-fcitx5-1760000000000000007"} {
		if !slices.Contains(args, want) {
			t.Fatalf("缺少 %s：%q", want, args)
		}
	}
	for _, bad := range []string{"--scope", "-d", "--setenv=DISPLAY=:0", "--setenv=BAD-NAME=x"} {
		if slices.Contains(args, bad) {
			t.Fatalf("不該有 %s：%q", bad, args)
		}
	}
	if tail := args[len(args)-3:]; strings.Join(tail, " ") != "-- fcitx5 --disable=wayland" {
		t.Fatalf("命令：%q", tail)
	}
	for _, a := range args {
		if strings.Contains(a, "MULTI") || strings.Contains(a, "Nice") || strings.Contains(a, "IOScheduling") {
			t.Fatalf("%q", args)
		}
	}
}
