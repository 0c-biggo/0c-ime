package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/robustio"
)

// denyRenames 讓 match 認得的改名前 n 次回 ERROR_ACCESS_DENIED（n < 0 表示一直拒絕），模擬端點防護
// 掃描剛寫入的檔案時開著目錄裡的檔案（#329）。回傳被拒絕的次數。
func denyRenames(t *testing.T, n int, match func(from, to string) bool) *int {
	t.Helper()
	denied := 0
	restore := robustio.SetForTest(func(from, to string) error {
		if match(from, to) && (n < 0 || denied < n) {
			denied++
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: robustio.AccessDenied}
		}
		return os.Rename(from, to)
	}, nil)
	t.Cleanup(restore)
	return &denied
}

func isStage(from, _ string) bool { return strings.HasPrefix(filepath.Base(from), ".stage-") }

// #329：.stage-* 改名成版本目錄時被短暫擋下，等一下再改名就成功，不必整個重新安裝。
func TestWindowsInstallRetriesStageRenameDeniedByScanner(t *testing.T) {
	base := t.TempDir()
	denied := denyRenames(t, 5, isStage)
	r := &fakeRegistrar{}
	dir, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatalf("transient denial must be retried: %v", err)
	}
	if *denied != 5 {
		t.Fatalf("denied %d times, want 5", *denied)
	}
	if _, err := Verify(dir, "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	if r.path != filepath.Join(dir, "0c_tsf.dll") {
		t.Fatal("not registered")
	}
	assertNoStage(t, filepath.Dir(dir))
}

// 一直被擋時回報原本的錯誤，不動目前的安裝，也不留下暫存目錄；擋住的程式放手後再裝一次就好。
func TestWindowsInstallReportsPersistentStageDenial(t *testing.T) {
	base := t.TempDir()
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
	denied := denyRenames(t, -1, isStage)
	if _, _, err = InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r); !errors.Is(err, robustio.AccessDenied) {
		t.Fatalf("want ERROR_ACCESS_DENIED, got %v", err)
	}
	if *denied < 30 {
		t.Fatalf("only %d attempts before giving up", *denied)
	}
	if after, _ := os.ReadFile(filepath.Join(base, "install-receipt.json")); string(after) != string(receipt) || r.path != filepath.Join(first, "0c_tsf.dll") {
		t.Fatal("failed install changed the current installation")
	}
	assertNoStage(t, filepath.Dir(first))
	if _, err := os.Stat(filepath.Join(base, "update.lock")); !os.IsNotExist(err) {
		t.Fatal("install lock left behind")
	}
}

// 不完整的版本目錄移開成 .broken-* 時同樣會被掃描擋下（剛裝到一半的檔案）。
func TestWindowsInstallRetriesBrokenDirectoryMove(t *testing.T) {
	base := t.TempDir()
	bundle := bundleFixture(t, "0.1.0-dev.1")
	dir, _, err := InstallWindows(bundle, base, "amd64", &fakeRegistrar{})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(base, "install-receipt.json"))
	os.Remove(filepath.Join(dir, "data", "lm.bin"))
	denied := denyRenames(t, 3, func(from, to string) bool { return strings.Contains(filepath.Base(to), ".broken-") })
	if _, _, err = InstallWindows(bundle, base, "amd64", &fakeRegistrar{}); err != nil {
		t.Fatalf("transient denial moving the broken directory must be retried: %v", err)
	}
	if *denied != 3 {
		t.Fatalf("denied %d times, want 3", *denied)
	}
	if _, err := Verify(dir, "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
}

// 收據（install-receipt.json）以暫存檔改名覆蓋；防毒開著舊收據時同樣是暫時性的拒絕。
func TestWindowsInstallRetriesReceiptReplace(t *testing.T) {
	base := t.TempDir()
	denied := denyRenames(t, 2, func(_, to string) bool { return filepath.Base(to) == "install-receipt.json" })
	if _, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", &fakeRegistrar{}); err != nil {
		t.Fatal(err)
	}
	if *denied != 2 {
		t.Fatalf("denied %d times, want 2", *denied)
	}
	if _, err := readReceipt(filepath.Join(base, "install-receipt.json")); err != nil {
		t.Fatal(err)
	}
}

// 回復時還原個人詞庫（暫存檔改名蓋過 user.json）也重試。
func TestWindowsRollbackRetriesUserStoreRestore(t *testing.T) {
	base := t.TempDir()
	user := filepath.Join(base, UserStore)
	r := &fakeRegistrar{}
	first, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.1"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(user, []byte("before upgrade"), 0600)
	if _, _, err = InstallWindows(bundleFixture(t, "0.1.0-dev.2"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(user, []byte("written by newer version"), 0600)
	denied := denyRenames(t, 4, func(from, to string) bool {
		return strings.HasPrefix(filepath.Base(from), ".user-restore-")
	})
	if _, err = RollbackWindows(base, r, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(user); string(b) != "before upgrade" || r.path != filepath.Join(first, "0c_tsf.dll") || *denied != 4 {
		t.Fatalf("restore %q, registration %s, denied %d", b, r.path, *denied)
	}
}

func assertNoStage(t *testing.T, versions string) {
	t.Helper()
	entries, _ := os.ReadDir(versions)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".stage-") {
			t.Fatalf("stage directory left behind: %s", e.Name())
		}
	}
}
