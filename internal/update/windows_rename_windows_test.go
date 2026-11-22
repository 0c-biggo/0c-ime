package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/robustio"
)

// #329 真機版：安裝流程改名 .stage-* 的那一刻，暫存目錄裡有個檔案被開著（像端點防護掃描剛寫入的檔案），
// 真的 MoveFileEx 回 ERROR_ACCESS_DENIED；檔案 300 ms 後關掉，安裝要在原地等到改名成功。
func TestWindowsInstallWaitsForScannerHoldingStagedFile(t *testing.T) {
	base := t.TempDir()
	var held *os.File
	var attempts []error
	restore := robustio.SetForTest(func(from, to string) error {
		if strings.HasPrefix(filepath.Base(from), ".stage-") && held == nil {
			f, err := os.Open(filepath.Join(from, "data", "lm.bin"))
			if err != nil {
				t.Errorf("open staged file: %v", err)
			} else {
				held = f
				time.AfterFunc(300*time.Millisecond, func() { f.Close() })
			}
		}
		err := os.Rename(from, to)
		if strings.HasPrefix(filepath.Base(from), ".stage-") {
			attempts = append(attempts, err)
		}
		return err
	}, time.Sleep)
	defer restore()
	start := time.Now()
	r := &fakeRegistrar{}
	dir, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatalf("install must wait for the scanner: %v (attempts %v)", err, attempts)
	}
	t.Logf("stage renamed after %d attempts, install took %s; first error: %v", len(attempts), time.Since(start).Round(time.Millisecond), attempts[0])
	if len(attempts) < 2 || !errors.Is(attempts[0], robustio.AccessDenied) {
		t.Fatalf("expected a real ERROR_ACCESS_DENIED first, got %v", attempts)
	}
	if _, err := Verify(dir, "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(dir, "0c_tsf.dll") {
		t.Fatal("not registered")
	}
	assertNoStage(t, filepath.Dir(dir))
}
