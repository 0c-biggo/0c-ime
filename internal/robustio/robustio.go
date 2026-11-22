// Package robustio 包住 Windows 上會被其他程式短暫擋下的檔案操作（#329）。
//
// Windows 改名目錄時，目錄裡只要有任何檔案被別的行程開著，MoveFileEx 就回 ERROR_ACCESS_DENIED；端點防護
// （例如 Microsoft Defender for Endpoint 的 MsSense.exe）掃描剛寫入的檔案時會開檔約 0.1 ms，剛複製好上千個檔案
// （含內附 Python）的暫存目錄很容易在改名的那一刻被擋下。檔案改名遇到對方沒給 FILE_SHARE_DELETE 時則是
// ERROR_SHARING_VIOLATION，位元組範圍被鎖住是 ERROR_LOCK_VIOLATION。這些等一下就會好，所以有限次、遞增等待重試；
// 其他錯誤（找不到、路徑錯、目的地是非空目錄……）立刻回傳。作法同 Go 的 cmd/internal/robustio
// （ACCESS_DENIED、SHARING_VIOLATION 重試約 2 秒）與 node-graceful-fs 在 win32 的 rename（EACCES、EPERM、EBUSY）。
//
// 拒絕若是真的權限問題，最多多等 RetryBudget 才回報；非 Windows 平台不重試，行為同 os.Rename。
package robustio

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
)

// Windows 的錯誤碼（syscall 套件只在 Windows 定義後兩個名稱，這裡用數值，讓判斷在任何平台都能測）。
const (
	AccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	SharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	LockViolation    = syscall.Errno(33) // ERROR_LOCK_VIOLATION
)

// RetryBudget 是重試時累計等待的上限。防毒掃描一個檔案約 0.1 ms，但剛寫好的整個目錄會被逐檔掃描，
// 改名要等到兩次開檔之間的空檔；Go 的 2 秒是給建置快取裡單一檔案用的，這裡取 10 秒涵蓋整個版本目錄。
const RetryBudget = 10 * time.Second

const (
	firstDelay = 10 * time.Millisecond
	maxDelay   = 250 * time.Millisecond // 上限不要太長：掃描是一個接一個檔案，越常試越容易落在空檔
)

var (
	retrying = runtime.GOOS == "windows"
	rename   = os.Rename
	sleep    = time.Sleep
)

// Transient 回報 err 是不是等一下就可能消失的 Windows 拒絕。
func Transient(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case AccessDenied, SharingViolation, LockViolation:
		return true
	}
	return false
}

// Retry 執行 op；在 Windows 上 op 回傳 Transient 錯誤時以 10 ms 起、倍增、最長 250 ms 的間隔重試，
// 累計等待超過 RetryBudget 就回傳最後一次的錯誤（仍可用 errors.Is／errors.As 取得原本的錯誤）。
func Retry(op func() error) error {
	err := op()
	if err == nil || !retrying {
		return err
	}
	attempts := 1
	var waited time.Duration
	for delay := firstDelay; Transient(err); delay = min(delay*2, maxDelay) {
		if waited+delay > RetryBudget {
			return fmt.Errorf("%w (still denied after %d attempts over %s; another program such as antivirus may be holding a file inside)", err, attempts, waited.Round(time.Millisecond))
		}
		sleep(delay)
		waited += delay
		attempts++
		if err = op(); err == nil {
			return nil
		}
	}
	return err
}

// Rename 同 os.Rename；在 Windows 上遇到暫時性拒絕時依 Retry 重試。
func Rename(oldpath, newpath string) error {
	return Retry(func() error { return rename(oldpath, newpath) })
}

// SetForTest 讓其他套件的測試在任何平台走重試邏輯：r 取代 os.Rename（nil 表示不換）、s 取代等待
// （nil 表示不等）。回傳還原函式。只給測試用。
func SetForTest(r func(oldpath, newpath string) error, s func(time.Duration)) (restore func()) {
	oldRetrying, oldRename, oldSleep := retrying, rename, sleep
	retrying = true
	if r != nil {
		rename = r
	}
	if s == nil {
		s = func(time.Duration) {}
	}
	sleep = s
	return func() { retrying, rename, sleep = oldRetrying, oldRename, oldSleep }
}
