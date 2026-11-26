package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// watchMacApp 在每一次搬移、交換之後檢查 Input Methods/0c.app 還在不在，記下 App 不在原路徑的步驟（#354）。
// LaunchServices／TextInputMenuAgent 可能正好在這個空檔掃描，把輸入來源登記拿掉（#351）。
func watchMacApp(t *testing.T, p MacPaths, swap func(a, b string) error) *[]string {
	t.Helper()
	var gaps []string
	check := func(step string) {
		if _, err := os.Lstat(p.app()); err != nil {
			gaps = append(gaps, step)
		}
	}
	oldRename, oldSwap := macRename, macSwap
	t.Cleanup(func() { macRename, macSwap = oldRename, oldSwap })
	macRename = func(a, b string) error {
		err := os.Rename(a, b)
		check("rename " + filepath.Base(filepath.Dir(a)) + "/" + filepath.Base(a) + " → " + filepath.Base(filepath.Dir(b)) + "/" + filepath.Base(b))
		return err
	}
	macSwap = func(a, b string) error {
		err := swap(a, b)
		check("swap " + a + " ↔ " + b)
		return err
	}
	return &gaps
}

func macTestPaths(t *testing.T) MacPaths {
	home := t.TempDir()
	return MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
}

func backupVersion(t *testing.T, p MacPaths, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.Support, filepath.FromSlash(rel), MacApp, "Contents/MacOS/0c-server"))
	if err != nil {
		return "absent"
	}
	return strings.Fields(string(b))[0]
}

// 同一個卷宗時更新與回復以交換換掉 0c.app：過程中原路徑一直有 App，舊版留在備份當回復來源（#354）。
func TestMacInstallSwapsAppWithoutGap(t *testing.T) {
	p := macTestPaths(t)
	if _, _, err := InstallMac(macBundle(t, "0.2.14"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	probe := t.TempDir()
	os.Mkdir(filepath.Join(probe, "a"), 0o700)
	os.Mkdir(filepath.Join(probe, "b"), 0o700)
	if err := swapPaths(filepath.Join(probe, "a"), filepath.Join(probe, "b")); err != nil {
		t.Skipf("file system cannot swap: %v", err)
	}
	gaps := watchMacApp(t, p, swapPaths)
	if _, _, err := InstallMac(macBundle(t, "0.2.15"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if len(*gaps) > 0 {
		t.Fatalf("0c.app was missing after: %v", *gaps)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.15 ") {
		t.Fatalf("installed %q", v)
	}
	r, err := readMacReceipt(p.receipt())
	if err != nil || r.Version != "0.2.15" || backupVersion(t, p, r.Previous) != "0.2.14" {
		t.Fatalf("receipt %+v %v; backup %s", r, err, backupVersion(t, p, r.Previous))
	}
	if _, err := MacRollbackTarget(p); err != nil {
		t.Fatal(err)
	}
	// 回復也以交換換回，剛換下的版本留在新的備份，可再回復。
	rb, err := RollbackMac(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(*gaps) > 0 {
		t.Fatalf("0c.app was missing during rollback after: %v", *gaps)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.14 ") || rb.App != p.app() {
		t.Fatalf("rolled back to %q (%s)", v, rb.App)
	}
	if b, err := os.ReadFile(filepath.Join(rb.Backup, MacApp, "Contents/MacOS/0c-server")); err != nil || !strings.HasPrefix(string(b), "0.2.15 ") {
		t.Fatalf("rollback backup %q %v", b, err)
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.15 ") || len(*gaps) > 0 {
		t.Fatalf("second rollback %q, gaps %v", v, *gaps)
	}
	// 每次只留收據指到的那一份備份。
	if dirs, _ := filepath.Glob(filepath.Join(p.Support, "backups", "macos-*")); len(dirs) != 1 {
		t.Fatalf("backups %v", dirs)
	}
	if left, _ := filepath.Glob(filepath.Join(p.Support, ".stage-*")); len(left) != 0 {
		t.Fatalf("stage left %v", left)
	}
}

// 不同磁碟或檔案系統不支援交換時退回原本的做法，安裝與回復結果相同。
func TestMacInstallFallsBackWhenSwapUnsupported(t *testing.T) {
	for _, cause := range []error{syscall.ENOTSUP, syscall.EXDEV, syscall.EINVAL, errors.ErrUnsupported} {
		t.Run(cause.Error(), func(t *testing.T) {
			p := macTestPaths(t)
			if _, _, err := InstallMac(macBundle(t, "0.2.14"), "arm64", p); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			watchMacApp(t, p, func(a, b string) error { calls.Add(1); return cause })
			if _, _, err := InstallMac(macBundle(t, "0.2.15"), "arm64", p); err != nil {
				t.Fatal(err)
			}
			if calls.Load() == 0 {
				t.Fatal("did not try to swap")
			}
			r, err := readMacReceipt(p.receipt())
			if err != nil || !strings.HasPrefix(appVersion(t, p), "0.2.15 ") || backupVersion(t, p, r.Previous) != "0.2.14" {
				t.Fatalf("installed %q, receipt %+v %v", appVersion(t, p), r, err)
			}
			if _, err := RollbackMac(p); err != nil || !strings.HasPrefix(appVersion(t, p), "0.2.14 ") {
				t.Fatalf("rollback %q %v", appVersion(t, p), err)
			}
			if left, _ := filepath.Glob(filepath.Join(p.Support, ".stage-*")); len(left) != 0 {
				t.Fatalf("stage left %v", left)
			}
		})
	}
}

// 交換之後寫收據失敗：交換回去，原路徑仍是舊版、沒有空檔，收據不變。
func TestMacInstallSwapsBackWhenReceiptFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no RENAME_SWAP, and directory permissions do not block writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	p := macTestPaths(t)
	if _, _, err := InstallMac(macBundle(t, "0.2.14"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	swaps := 0
	gaps := watchMacApp(t, p, func(a, b string) error {
		err := swapPaths(a, b)
		if err == nil && swaps == 0 {
			os.Chmod(p.Support, 0o500) // 收據寫不進去
		}
		swaps++
		return err
	})
	t.Cleanup(func() { os.Chmod(p.Support, 0o700) })
	if _, _, err := InstallMac(macBundle(t, "0.2.15"), "arm64", p); err == nil {
		t.Fatal("install succeeded without a receipt")
	}
	os.Chmod(p.Support, 0o700)
	if swaps == 0 {
		t.Skip("file system cannot swap")
	}
	if len(*gaps) > 0 {
		t.Fatalf("0c.app was missing after: %v", *gaps)
	}
	if v := appVersion(t, p); !strings.HasPrefix(v, "0.2.14 ") {
		t.Fatalf("left %q installed", v)
	}
	if r, err := readMacReceipt(p.receipt()); err != nil || r.Version != "0.2.14" {
		t.Fatalf("receipt %+v %v", r, err)
	}
	if changed, err := CheckMac(p); changed != nil || err != nil {
		t.Fatalf("installed app does not match receipt: %v %v", changed, err)
	}
}

// 另一個執行緒在更新期間不停查 0c.app（模擬系統在交接時掃描），一次都不能撲空。
func TestMacInstallPathNeverMissingWhilePolled(t *testing.T) {
	p := macTestPaths(t)
	if _, _, err := InstallMac(macBundle(t, "0.2.14"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	probe := t.TempDir()
	os.Mkdir(filepath.Join(probe, "a"), 0o700)
	os.Mkdir(filepath.Join(probe, "b"), 0o700)
	if err := swapPaths(filepath.Join(probe, "a"), filepath.Join(probe, "b")); err != nil {
		t.Skipf("file system cannot swap: %v", err)
	}
	var misses, polls atomic.Int64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			polls.Add(1)
			if _, err := os.Stat(filepath.Join(p.app(), "Contents/Info.plist")); err != nil {
				misses.Add(1)
			}
		}
	}()
	for i, v := range []string{"0.2.15", "0.2.16", "0.2.17"} {
		if _, _, err := InstallMac(macBundle(t, v), "arm64", p); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if _, err := RollbackMac(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	close(stop)
	<-done
	if misses.Load() > 0 {
		t.Fatalf("0c.app missing in %d of %d polls", misses.Load(), polls.Load())
	}
	t.Logf("%d polls, no misses", polls.Load())
}

// 舊名 Zuin.app 不能交換：照舊移入備份，新的 0c.app 直接放入；回復時照原名放回。
func TestMacInstallOverZuinAppDoesNotSwap(t *testing.T) {
	p := macTestPaths(t)
	legacy := filepath.Join(p.appNamed(LegacyMacApp), "Contents/MacOS/zuin-server")
	os.MkdirAll(filepath.Dir(legacy), 0o755)
	os.WriteFile(legacy, []byte("zuin"), 0o755)
	var calls atomic.Int32
	watchMacApp(t, p, func(a, b string) error { calls.Add(1); return swapPaths(a, b) })
	if _, _, err := InstallMac(macBundle(t, "0.2.15"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("swapped although there was no 0c.app")
	}
	if _, err := os.Lstat(p.appNamed(LegacyMacApp)); !os.IsNotExist(err) {
		t.Fatal("Zuin.app left in Input Methods")
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(legacy); string(b) != "zuin" {
		t.Fatal("Zuin.app not restored")
	}
	if _, err := os.Lstat(p.app()); !os.IsNotExist(err) {
		t.Fatal("0c.app left after rolling back to Zuin.app")
	}
}
