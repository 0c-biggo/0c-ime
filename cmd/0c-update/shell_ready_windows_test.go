//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// #253：不再比登入時間。收據是這一版就算裝好（新引擎有沒有接手由協調器另外核對）；裝好後才登入或沒登入都一樣。
func TestShellReadyOnlyChecksTheReceipt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "install-receipt.json")
	for _, c := range []struct {
		receipt, version string
		ready, fails     bool
	}{
		{`{"version":"0.2.8","active":true}`, "0.2.8", true, false},
		{`{"version":"0.2.8"}`, "0.2.8", true, false},
		{`{"version":"0.2.7","active":true}`, "0.2.8", false, false},  // 回復了
		{`{"version":"0.2.8","active":false}`, "0.2.8", false, false}, // 解除安裝了
		{`{`, "0.2.8", false, true},
	} {
		if err := os.WriteFile(p, []byte(c.receipt), 0600); err != nil {
			t.Fatal(err)
		}
		ready, err := shellReadyAfterInstall(p, c.version)
		if ready != c.ready || (err != nil) != c.fails {
			t.Fatalf("%s：ready=%v err=%v", c.receipt, ready, err)
		}
	}
	if ready, err := shellReadyAfterInstall(p+".missing", "0.2.8"); ready || err == nil {
		t.Fatalf("沒有收據：ready=%v err=%v", ready, err)
	}
}

// #253：Windows 裝好後協調器不等登入。
func TestWindowsShellDoesNotWaitForLogin(t *testing.T) {
	if shellPendingAfterInstall() {
		t.Fatal("Windows 裝好不該回 pending")
	}
	if !(&system{}).ShellActiveWithoutRelogin() || !shellWithoutRelogin {
		t.Fatal("Windows 要走不等重新登入的路徑")
	}
}
