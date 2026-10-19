package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// vocab watch 要在啟動它的父行程被強制結束後自己離開（Windows 不會改父行程編號，所以另外拿 handle）。
// 測試以自己的執行檔扮演三層：中間層啟動葉節點後立即結束，葉節點應察覺父行程已經不在。
func TestParentWatchNoticesParentExit(t *testing.T) {
	switch os.Getenv("ZC_PARENT_ROLE") {
	case "middle":
		leaf := exec.Command(os.Args[0], "-test.run=TestParentWatchNoticesParentExit")
		leaf.Env = append(os.Environ(), "ZC_PARENT_ROLE=leaf")
		if err := leaf.Start(); err != nil {
			os.Exit(2)
		}
		time.Sleep(500 * time.Millisecond) // 讓葉節點先拿到父行程的 handle
		os.Exit(0)
	case "leaf":
		gone := parentWatch()
		if gone() {
			os.WriteFile(os.Getenv("ZC_PARENT_OUT"), []byte("early"), 0o600)
			os.Exit(0)
		}
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if gone() {
				os.WriteFile(os.Getenv("ZC_PARENT_OUT"), []byte("gone"), 0o600)
				os.Exit(0)
			}
		}
		os.WriteFile(os.Getenv("ZC_PARENT_OUT"), []byte("timeout"), 0o600)
		os.Exit(0)
	}
	out := filepath.Join(t.TempDir(), "result")
	middle := exec.Command(os.Args[0], "-test.run=TestParentWatchNoticesParentExit")
	middle.Env = append(os.Environ(), "ZC_PARENT_ROLE=middle", "ZC_PARENT_OUT="+out)
	if err := middle.Run(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(out); err == nil {
			if string(b) != "gone" {
				t.Fatalf("葉節點：%s", b)
			}
			return
		}
	}
	t.Fatal("葉節點沒有回報")
}
