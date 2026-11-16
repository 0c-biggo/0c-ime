package main

import (
	"path/filepath"
	"testing"
)

func TestPlanUsesInstallBaseAndLogsUnderUpdateDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Zuin")
	exe := filepath.Join(base, "bin", "0c-updatew.exe")
	for _, args := range [][]string{{"auto"}, {"engine"}, {"protocol", "zc-update:finish?sha256=ab&version=0.2.12"}} {
		got, log, err := plan(exe, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != base {
			t.Fatalf("%v: base %s", args, got)
		}
		if log != filepath.Join(base, "update", args[0]+"-last.log") {
			t.Fatalf("%v: log %s", args, log)
		}
	}
}

// 不是通用的隱藏視窗啟動器：其他子命令、多餘參數、別的協定都拒絕。
func TestPlanRejectsOtherCommands(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "bin", "0c-updatew.exe")
	for _, args := range [][]string{
		nil,
		{"uninstall"},
		{"install", "-bundle", `C:\x`},
		{"auto", "-feed", "https://example.invalid"},
		{"engine", "extra"},
		{"protocol"},
		{"protocol", "https://example.invalid/"},
		{"protocol", "zc-update:finish", "extra"},
	} {
		if _, _, err := plan(exe, args); err == nil {
			t.Fatalf("%v 應被拒絕", args)
		}
	}
	if _, _, err := plan("0c-updatew.exe", []string{"auto"}); err == nil {
		t.Fatal("相對路徑應被拒絕")
	}
}
