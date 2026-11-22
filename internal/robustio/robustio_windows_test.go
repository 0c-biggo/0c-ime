package robustio

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 真的 NTFS：目錄裡有檔案開著時改名目錄回 ERROR_ACCESS_DENIED（#329 回報者以 Process Monitor 看到的情況），
// 開著的檔案關掉後 Rename 接著成功，內容完整。
func TestRenameWaitsForOpenFileInsideDirectory(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, ".stage-1")
	file := filepath.Join(stage, "neural", "python", "_remote_debugging.pyd")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("pyd"), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "0.2.13-abc")
	if err := os.Rename(stage, dest); !errors.Is(err, AccessDenied) {
		held.Close()
		t.Fatalf("plain os.Rename with an open file inside: want ERROR_ACCESS_DENIED, got %v", err)
	}
	var attempts []error
	restore := SetForTest(func(o, n string) error {
		err := os.Rename(o, n)
		attempts = append(attempts, err)
		return err
	}, time.Sleep)
	defer restore()
	time.AfterFunc(300*time.Millisecond, func() { held.Close() })
	start := time.Now()
	if err := Rename(stage, dest); err != nil {
		t.Fatalf("Rename must succeed once the file is closed: %v", err)
	}
	t.Logf("succeeded after %d attempts in %s; first error: %v", len(attempts), time.Since(start).Round(time.Millisecond), attempts[0])
	if len(attempts) < 2 || !errors.Is(attempts[0], AccessDenied) {
		t.Fatalf("expected an initial ERROR_ACCESS_DENIED, got %v", attempts)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "neural", "python", "_remote_debugging.pyd")); err != nil || string(b) != "pyd" {
		t.Fatalf("content after rename: %q %v", b, err)
	}
}

// 一直開著：等滿 RetryBudget 後回 ERROR_ACCESS_DENIED，目錄原封不動。
func TestRenameGivesUpWhileFileStaysOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("waits the full retry budget")
	}
	dir := t.TempDir()
	stage := filepath.Join(dir, ".stage-2")
	file := filepath.Join(stage, "held.bin")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	start := time.Now()
	err = Rename(stage, filepath.Join(dir, "dest"))
	elapsed := time.Since(start)
	t.Logf("gave up after %s: %v", elapsed.Round(time.Millisecond), err)
	if !errors.Is(err, AccessDenied) {
		t.Fatalf("want ERROR_ACCESS_DENIED, got %v", err)
	}
	if elapsed < RetryBudget-time.Second || elapsed > RetryBudget+5*time.Second {
		t.Fatalf("elapsed %s, want about %s", elapsed, RetryBudget)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("source must be untouched")
	}
}

// 另一個行程以完整共用（含 FILE_SHARE_DELETE，像掃描程式那樣）開著目錄裡的檔案 500 ms：改名目錄仍被拒絕，
// 對方關檔後 Rename 成功。
func TestRenameWaitsForOtherProcessHoldingFile(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, ".stage-3")
	file := filepath.Join(stage, "neural", "python", "_remote_debugging.pyd")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("pyd"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(dir, "ready")
	script := `$f = [System.IO.File]::Open($env:HOLD_FILE, 'Open', 'Read', 'ReadWrite, Delete'); ` +
		`[System.IO.File]::WriteAllText($env:HOLD_READY, 'x'); Start-Sleep -Milliseconds 500; $f.Close()`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "HOLD_FILE="+file, "HOLD_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Skipf("powershell: %v", err)
	}
	defer cmd.Wait()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("holder did not start")
		}
	}
	var attempts []error
	restore := SetForTest(func(o, n string) error {
		err := os.Rename(o, n)
		attempts = append(attempts, err)
		return err
	}, time.Sleep)
	defer restore()
	start := time.Now()
	dest := filepath.Join(dir, "0.2.13-abc")
	if err := Rename(stage, dest); err != nil {
		t.Fatalf("Rename must succeed once the other process closes the file: %v", err)
	}
	t.Logf("succeeded after %d attempts in %s; first error: %v", len(attempts), time.Since(start).Round(time.Millisecond), attempts[0])
	if len(attempts) < 2 || !errors.Is(attempts[0], AccessDenied) {
		t.Fatalf("expected an initial ERROR_ACCESS_DENIED, got %v", attempts)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "neural", "python", "_remote_debugging.pyd")); err != nil || string(b) != "pyd" {
		t.Fatalf("content after rename: %q %v", b, err)
	}
}
