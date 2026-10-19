//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestElevationOriginRejectsBeforeWriting(t *testing.T) {
	sid, err := processUserSID()
	if err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	base := basePath()
	for _, args := range [][]string{
		{"-target", "0.1.0-dev.350", "-target-sha256", strings.Repeat("a", 64), "-origin-user", "S-1-5-21-0-0-0-1234", "-origin-base", base},
		{"-origin-user", sid, "-origin-base", filepath.Join(local, "other")},
		{"-origin-user", sid},
		{"-origin-base", base},
		{"-origin-user", sid, "-origin-base", "relative"},
	} {
		if err := autoCommand("finish", args); err == nil {
			t.Fatalf("accepted invalid UAC handoff: %v", args)
		}
		entries, err := os.ReadDir(local)
		if err != nil || len(entries) != 0 {
			t.Fatalf("invalid handoff touched user data: %v %v", entries, err)
		}
	}
	if err := validateElevationOrigin("auto", sid, base); err == nil {
		t.Fatal("handoff must only execute finish")
	}
	if err := validateElevationOrigin("finish", sid, base); (err == nil) != elevatedOK() {
		t.Fatalf("same user/base must require an elevated token: %v", err)
	}
	if err := validateElevationOrigin("finish", "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestElevationCancellationKeepsPending(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	base := basePath()
	stateDir := filepath.Join(base, "update")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(stateDir, "state.json")
	before := []byte(`{"stage":"prepared","blocked":{"reason":"needs_consent"}}`)
	if err := os.WriteFile(stateFile, before, 0600); err != nil {
		t.Fatal(err)
	}
	sid, err := processUserSID()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"finish", "-feed", "https://example.test/含空白 feed.json", "-target", "0.1.0-dev.350", "-target-sha256", strings.Repeat("a", 64)}
	err = relaunchElevatedWith(args, func(file string, got []string) (windows.Handle, error) {
		want := append(append([]string{}, args...), "-origin-user", sid, "-origin-base", base)
		if !filepath.IsAbs(file) || !reflect.DeepEqual(got, want) {
			t.Fatalf("incorrect UAC handoff: %s %v", file, got)
		}
		return 0, windows.ERROR_CANCELLED
	})
	if !errors.Is(err, windows.ERROR_CANCELLED) || !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("cancellation was not reported: %v", err)
	}
	after, err := os.ReadFile(stateFile)
	if err != nil || string(after) != string(before) {
		t.Fatalf("cancellation changed pending update: %s %v", after, err)
	}
}

// 真實子程序／Win32 handle，驗證啟動成功但子程序失敗時不能回報成功。
// 不觸發 UAC；安全桌面的同意／取消仍需獨立桌面驗收。
func TestElevationWaitsForResult(t *testing.T) {
	for _, fail := range []bool{false, true} {
		err := relaunchElevatedWith([]string{"finish"}, func(_ string, _ []string) (windows.Handle, error) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestElevationChild$")
			cmd.Env = append(os.Environ(), "ZC_ELEVATION_TEST_CHILD=success")
			if fail {
				cmd.Env = append(os.Environ(), "ZC_ELEVATION_TEST_CHILD=failure")
			}
			if err := cmd.Start(); err != nil {
				return 0, err
			}
			h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
			go cmd.Wait()
			return h, err
		})
		if (err != nil) != fail {
			t.Fatalf("child failure=%v, result=%v", fail, err)
		}
		if fail && !strings.Contains(err.Error(), "結束碼 7") {
			t.Fatalf("lost child result: %v", err)
		}
	}
}

func TestElevationChild(t *testing.T) {
	switch os.Getenv("ZC_ELEVATION_TEST_CHILD") {
	case "success":
		os.Exit(0)
	case "failure":
		os.Exit(7)
	}
}

func TestShellExecuteInfoLayout(t *testing.T) {
	// x64 Windows 公開套件 ABI：SHELLEXECUTEINFOW.cbSize=112。
	info := shellExecuteInfo{}
	if reflect.TypeOf(info).Size() != 112 || reflect.TypeOf(info).Field(14).Offset != 104 {
		t.Fatal("incorrect SHELLEXECUTEINFOW ABI")
	}
}
