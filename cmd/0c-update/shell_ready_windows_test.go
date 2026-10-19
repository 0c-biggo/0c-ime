//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

func TestShellReadyRequiresReceiptVersionAndLaterDesktopLogin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "install-receipt.json")
	if err := os.WriteFile(p, []byte(`{"version":"0.1.0-dev.350"}`), 0600); err != nil {
		t.Fatal(err)
	}
	installed := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, installed, installed); err != nil {
		t.Fatal(err)
	}
	for _, logon := range []time.Time{{}, installed.Add(-time.Minute), installed} {
		ready, err := shellReadyAfterInstall(p, "0.1.0-dev.350", logon)
		if err != nil || ready {
			t.Fatalf("old or unknown login: ready=%v err=%v", ready, err)
		}
	}
	ready, err := shellReadyAfterInstall(p, "0.1.0-dev.350", installed.Add(time.Minute))
	if err != nil || !ready {
		t.Fatalf("new login: ready=%v err=%v", ready, err)
	}
	if ready, err = shellReadyAfterInstall(p, "0.1.0-dev.349", installed.Add(time.Minute)); ready || err == nil {
		t.Fatalf("wrong receipt: ready=%v err=%v", ready, err)
	}
	if err := os.WriteFile(p, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if ready, err = shellReadyAfterInstall(p, "0.1.0-dev.350", time.Now()); ready || err == nil {
		t.Fatalf("invalid receipt: ready=%v err=%v", ready, err)
	}
}

func TestWTSSessionInfoLayoutAndNativeQuery(t *testing.T) {
	var info wtsSessionInfo
	if unsafe.Sizeof(info) != 216 || unsafe.Offsetof(info.LogonTime) != 200 {
		t.Fatalf("WTSINFOW ABI size=%d logon=%d", unsafe.Sizeof(info), unsafe.Offsetof(info.LogonTime))
	}
	logon, err := desktopLogonTime()
	// SSH/CI live in session 0; failure must remain unknown, never a successful login.
	if err != nil {
		if !logon.IsZero() {
			t.Fatalf("failed query returned logon=%s", logon)
		}
		t.Logf("no interactive login: %v", err)
		return
	}
	if logon.IsZero() || logon.After(time.Now()) {
		t.Fatalf("invalid native login: %s", logon)
	}
	t.Logf("interactive desktop login: %s", logon)
}
