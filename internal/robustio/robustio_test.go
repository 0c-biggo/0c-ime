package robustio

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func denied(errno syscall.Errno) error {
	return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errno}
}

// 前幾次被防毒開檔擋下（ERROR_ACCESS_DENIED），之後成功：不回報錯誤，等待時間遞增。
func TestRenameRetriesTransientDenialThenSucceeds(t *testing.T) {
	for _, errno := range []syscall.Errno{AccessDenied, SharingViolation, LockViolation} {
		calls := 0
		var waits []time.Duration
		restore := SetForTest(func(o, n string) error {
			calls++
			if calls <= 4 {
				return denied(errno)
			}
			return nil
		}, func(d time.Duration) { waits = append(waits, d) })
		err := Rename("stage", "dest")
		restore()
		if err != nil {
			t.Fatalf("errno %d: %v", errno, err)
		}
		if calls != 5 {
			t.Fatalf("errno %d: %d calls, want 5", errno, calls)
		}
		want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}
		if len(waits) != len(want) {
			t.Fatalf("waits %v, want %v", waits, want)
		}
		for i := range want {
			if waits[i] != want[i] {
				t.Fatalf("waits %v, want %v", waits, want)
			}
		}
	}
}

// 一直被擋：累計等待不超過 RetryBudget 就放棄，回傳的錯誤仍是原本的錯誤碼。
func TestRenameGivesUpAfterBudget(t *testing.T) {
	calls := 0
	var waited time.Duration
	restore := SetForTest(func(o, n string) error {
		calls++
		return denied(AccessDenied)
	}, func(d time.Duration) {
		if d > maxDelay {
			t.Fatalf("delay %s over cap", d)
		}
		waited += d
	})
	defer restore()
	err := Rename("stage", "dest")
	if err == nil {
		t.Fatal("must fail")
	}
	if !errors.Is(err, AccessDenied) {
		t.Fatalf("original errno lost: %v", err)
	}
	var link *os.LinkError
	if !errors.As(err, &link) {
		t.Fatalf("LinkError lost: %v", err)
	}
	if waited > RetryBudget || waited < RetryBudget-maxDelay {
		t.Fatalf("waited %s, want close to %s", waited, RetryBudget)
	}
	if calls < 30 {
		t.Fatalf("only %d attempts within the budget", calls)
	}
}

// 其他錯誤（找不到、目的地是非空目錄）立刻回報，不等。
func TestRenameDoesNotRetryOtherErrors(t *testing.T) {
	for _, e := range []error{denied(syscall.Errno(2)), denied(syscall.Errno(145)), errors.New("plain")} {
		calls := 0
		restore := SetForTest(func(o, n string) error { calls++; return e }, func(time.Duration) { t.Fatal("must not wait") })
		err := Rename("a", "b")
		restore()
		if err != e || calls != 1 {
			t.Fatalf("%v: err %v after %d calls", e, err, calls)
		}
	}
}

// 錯誤碼換成別的之後就不再重試（例如擋住的程式關檔後，目的地卻已經存在）。
func TestRetryStopsWhenErrorBecomesPermanent(t *testing.T) {
	calls := 0
	exists := denied(syscall.Errno(183)) // ERROR_ALREADY_EXISTS
	restore := SetForTest(func(o, n string) error {
		calls++
		if calls == 1 {
			return denied(SharingViolation)
		}
		return exists
	}, nil)
	defer restore()
	if err := Rename("a", "b"); err != exists || calls != 2 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}

// 非 Windows 不重試；真的檔案系統上照常改名。
func TestRenameRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "stage"), filepath.Join(dir, "dest")
	if err := os.MkdirAll(filepath.Join(from, "neural"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "neural")); err != nil {
		t.Fatal(err)
	}
	if err := Rename(from, to); err == nil {
		t.Fatal("missing source must fail")
	}
}
