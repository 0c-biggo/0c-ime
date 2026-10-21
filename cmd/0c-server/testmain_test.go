package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain 讓整個測試行程（以及它啟動的 0c-server 子行程）用臨時的家目錄：引擎啟動時會清 onnxruntime 的遙測暫存
// （neural.RemoveTelemetryQueue），不可以動到跑測試的人自己的家目錄。個別測試仍可另外指定 HOME。
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "0c-server-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
