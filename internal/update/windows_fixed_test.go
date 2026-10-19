package update

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// 固定路徑的 TSF DLL（#121）：InprocServer32 永遠是 %LOCALAPPDATA%\Zuin\tsf\0c_tsf.dll。

// loadedFiles 模擬被應用程式載入的 DLL：Windows 上可以改名，但刪不掉。以內容辨認，改名後仍是同一個檔案。
type loadedFiles map[string]bool

func (l loadedFiles) load(t *testing.T, file string) {
	t.Helper()
	sum, err := Hash(file)
	if err != nil {
		t.Fatal(err)
	}
	l[sum] = true
}

// fakeFixedIO 替換改名與刪除：被載入的檔案刪不掉；failRename 決定第幾次改名（依 from 的檔名結尾）失敗。
func fakeFixedIO(t *testing.T, loaded loadedFiles, failRename func(from, to string) bool) {
	t.Helper()
	rename, remove, delay := fixedRename, fixedRemove, fixedRetryDelay
	t.Cleanup(func() { fixedRename, fixedRemove, fixedRetryDelay = rename, remove, delay })
	fixedRetryDelay = 0
	fixedRemove = func(p string) error {
		if sum, err := Hash(p); err == nil && loaded[sum] {
			return errors.New("file is in use")
		}
		return os.Remove(p)
	}
	fixedRename = func(from, to string) error {
		if failRename != nil && failRename(from, to) {
			return errors.New("access denied (antivirus scan)")
		}
		return os.Rename(from, to)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fixedEntries(t *testing.T, base string) []string {
	t.Helper()
	var names []string
	filepath.WalkDir(filepath.Join(base, FixedDir), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(filepath.Join(base, FixedDir), p)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(names)
	return names
}

func TestUsesFixedFromDev62(t *testing.T) {
	for version, want := range map[string]bool{"0.1.0-dev.61": false, "0.1.0-dev.61.1": false, "0.1.0-dev.62": true, "0.1.0-dev.62.1": true, "0.1.0": true, "0.1.0-dev.100": true} {
		r := &Receipt{Version: version, Directory: "versions/x", Files: map[string]string{"0c_tsf.dll": "", "0c-server.exe": "", "0c-ui.exe": ""}}
		if usesFixed(r) != want {
			t.Errorf("usesFixed(%s) = %v", version, !want)
		}
	}
	legacy := &Receipt{Version: "0.1.0-dev.62", Directory: "versions/x", Files: map[string]string{"zuin_tsf.dll": "", "zuin-server.exe": "", "zuin-ui.exe": ""}}
	if usesFixed(legacy) {
		t.Error("zuin-* layout cannot load from the fixed path")
	}
}

// 從 dev.61（版本目錄註冊）升級：InprocServer32 改指固定路徑；之後的更新路徑不變，換下的舊檔仍被載入時留著。
func TestFixedPathAcrossUpgradesWithLoadedOldFile(t *testing.T) {
	base := t.TempDir()
	loaded := loadedFiles{}
	fakeFixedIO(t, loaded, nil)
	r := &viewRegistrar{}
	old, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.61"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if r.x64 != filepath.Join(old, "0c_tsf.dll") {
		t.Fatalf("dev.61 keeps the version directory registration: %+v", r)
	}
	v62, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	fixed, fixed32 := FixedDLL(base), WindowsDLL32Of(FixedDLL(base))
	if r.x64 != fixed || r.x86 != fixed32 {
		t.Fatalf("first fixed-path install must register the fixed paths: %+v", r)
	}
	if readFile(t, fixed) != readFile(t, filepath.Join(v62, "0c_tsf.dll")) || readFile(t, fixed32) != readFile(t, filepath.Join(v62, "x86", "0c_tsf.dll")) {
		t.Fatal("fixed DLLs must be copies of the version directory")
	}
	// explorer 等仍載著 dev.62 的固定檔時更新到 dev.63，再更新到 dev.64：路徑不變，.old 名稱不撞。
	loaded.load(t, fixed)
	loaded.load(t, fixed32)
	v63, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.63"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if r.x64 != fixed || readFile(t, fixed) != readFile(t, filepath.Join(v63, "0c_tsf.dll")) {
		t.Fatal("upgrade must swap the fixed DLL in place")
	}
	loaded.load(t, fixed)
	if _, _, err = InstallWindows(x86Fixture(t, "0.1.0-dev.64"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	got := fixedEntries(t, base)
	want := []string{"0c_tsf.dll", "0c_tsf.dll.old-" + filepath.Base(v62), "0c_tsf.dll.old-" + filepath.Base(v63),
		"x86/0c_tsf.dll", "x86/0c_tsf.dll.old-" + filepath.Base(v62)}
	if !slices.Equal(got, want) {
		t.Fatalf("loaded old files must be kept under unique names, the unloaded one removed:\n got %v\nwant %v", got, want)
	}
	// 登出後（檔案不再被載入）下一次修復就清掉。
	clear(loaded)
	if _, err = RepairFixed(base); err != nil {
		t.Fatal(err)
	}
	if got := fixedEntries(t, base); !slices.Equal(got, []string{"0c_tsf.dll", "x86/0c_tsf.dll"}) {
		t.Fatalf("repair must remove old copies once unloaded: %v", got)
	}
}

func TestFixedOldNamesAreUnique(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "0c_tsf.dll")
	first := oldName(dst, "0.1.0-dev.62-abc")
	os.WriteFile(first, nil, 0600)
	second := oldName(dst, "0.1.0-dev.62-abc")
	if first == second || !strings.HasSuffix(second, ".old-0.1.0-dev.62-abc-2") {
		t.Fatalf("old names collide: %s %s", first, second)
	}
	if got := oldName(dst, "../x"); !strings.HasSuffix(got, ".old-previous") {
		t.Fatalf("unsafe tag must not reach the file name: %s", got)
	}
}

// 換檔停在每一個中間狀態後，RepairFixed（更新器每次執行、schedule repair）都能接著做完。
func TestRepairFixedResumesEveryIntermediateState(t *testing.T) {
	setup := func(t *testing.T) (string, string, string) {
		base := t.TempDir()
		fakeFixedIO(t, loadedFiles{}, nil)
		dir, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", &viewRegistrar{})
		if err != nil {
			t.Fatal(err)
		}
		return base, FixedDLL(base), readFile(t, filepath.Join(dir, "0c_tsf.dll"))
	}
	cases := map[string]func(t *testing.T, fixed, good string){
		// 準備好 .new 之前就中斷：固定檔還是上一版。
		"old fixed file, no staged copy": func(t *testing.T, fixed, good string) { os.WriteFile(fixed, []byte("previous release"), 0600) },
		// 準備好 .new、還沒開始改名。
		"old fixed file and staged copy": func(t *testing.T, fixed, good string) {
			os.WriteFile(fixed, []byte("previous release"), 0600)
			os.WriteFile(fixed+".new", []byte(good), 0600)
		},
		// 第一步（舊檔改名）完成、第二步（.new 改名）還沒做。
		"only staged copy": func(t *testing.T, fixed, good string) {
			os.Rename(fixed, fixed+".old-previous")
			os.WriteFile(fixed+".new", []byte(good), 0600)
		},
		// .new 寫到一半（雜湊不符）。
		"truncated staged copy": func(t *testing.T, fixed, good string) {
			os.Remove(fixed)
			os.WriteFile(fixed+".new", []byte(good[:3]), 0600)
		},
		// 換檔完成、.new 沒清掉（例如兩次更新器同時跑過）。
		"done but staged copy left": func(t *testing.T, fixed, good string) { os.WriteFile(fixed+".new", []byte("stale"), 0600) },
		// 防毒軟體刪掉或改動固定檔。
		"fixed file quarantined": func(t *testing.T, fixed, good string) { os.Remove(fixed) },
		"fixed file modified":    func(t *testing.T, fixed, good string) { os.WriteFile(fixed, []byte("tampered"), 0600) },
		"x86 fixed file deleted": func(t *testing.T, fixed, good string) { os.Remove(WindowsDLL32Of(fixed)) },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			base, fixed, good := setup(t)
			breakIt(t, fixed, good)
			changed, err := RepairFixed(base)
			if err != nil {
				t.Fatal(err)
			}
			if name != "done but staged copy left" && !changed {
				t.Error("repair must report that it redeployed")
			}
			if readFile(t, fixed) != good {
				t.Fatal("fixed DLL not restored")
			}
			if got := fixedEntries(t, base); !slices.Equal(got, []string{"0c_tsf.dll", "x86/0c_tsf.dll"}) {
				t.Fatalf("leftovers after repair: %v", got)
			}
		})
	}
}

func TestRepairFixedReportsQuarantinedSource(t *testing.T) {
	base := t.TempDir()
	fakeFixedIO(t, loadedFiles{}, nil)
	dir, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", &viewRegistrar{})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(FixedDLL(base))
	os.Remove(filepath.Join(dir, "0c_tsf.dll")) // 正本也被隔離：不能從別處補，要回報
	if _, err := RepairFixed(base); err == nil || !strings.Contains(err.Error(), "防毒") {
		t.Fatalf("missing source must be reported: %v", err)
	}
}

// 第一步失敗：維持舊版（註冊與收據都不變），下次再換；第二步失敗：放回舊檔。
func TestFixedSwapFailuresKeepPreviousVersion(t *testing.T) {
	for _, step := range []string{"first", "second", "second and put-back"} {
		t.Run(step, func(t *testing.T) {
			base := t.TempDir()
			failing := false
			fakeFixedIO(t, loadedFiles{}, func(from, to string) bool {
				if !failing {
					return false
				}
				switch step {
				case "first":
					return strings.HasSuffix(from, "0c_tsf.dll") && strings.Contains(to, ".old-")
				case "second":
					return strings.HasSuffix(from, ".new")
				default:
					return strings.HasSuffix(from, ".new") || strings.Contains(from, ".old-")
				}
			})
			r := &viewRegistrar{}
			v62, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
			failing = true
			_, _, err = InstallWindows(x86Fixture(t, "0.1.0-dev.63"), base, "amd64", r)
			if err == nil {
				t.Fatal("swap failure must fail the update")
			}
			after, _ := os.ReadFile(filepath.Join(base, "install-receipt.json"))
			if string(before) != string(after) {
				t.Fatal("receipt changed on failure")
			}
			if step == "second and put-back" {
				// 連舊檔都放不回去：固定路徑暫時沒有 DLL，下一次修復（不再失敗時）補回目前版本。
				if !strings.Contains(err.Error(), "也無法放回") {
					t.Fatalf("both failures must be reported: %v", err)
				}
				failing = false
				if _, err = RepairFixed(base); err != nil {
					t.Fatal(err)
				}
			}
			if readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(v62, "0c_tsf.dll")) {
				t.Fatal("fixed DLL must stay (or be restored to) the previous version")
			}
			if r.x64 != FixedDLL(base) {
				t.Fatalf("registration must be restored: %+v", r)
			}
			failing = false
			if _, _, err = InstallWindows(x86Fixture(t, "0.1.0-dev.63"), base, "amd64", r); err != nil {
				t.Fatalf("next attempt must succeed: %v", err)
			}
		})
	}
}

// 防毒掃描造成的暫時失敗：重試後成功。
func TestFixedSwapRetriesTransientFailures(t *testing.T) {
	base := t.TempDir()
	attempts := 0
	fakeFixedIO(t, loadedFiles{}, func(from, to string) bool {
		if strings.HasSuffix(from, ".new") {
			attempts++
			return attempts <= 3
		}
		return false
	})
	if _, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", &viewRegistrar{}); err != nil {
		t.Fatal(err)
	}
	if attempts < 4 {
		t.Fatalf("expected retries, got %d attempts", attempts)
	}
}

// 回復：dev.63 → dev.62 照樣換固定檔。
// 解除安裝移除固定檔與 .new／.old，仍被載入的留著；之後還能以 rollback 復原。
func TestFixedRollbackAndUninstall(t *testing.T) {
	base := t.TempDir()
	loaded := loadedFiles{}
	fakeFixedIO(t, loaded, nil)
	r := &viewRegistrar{}
	v62, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = InstallWindows(x86Fixture(t, "0.1.0-dev.63"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.x64 != FixedDLL(base) || readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(v62, "0c_tsf.dll")) {
		t.Fatal("rollback to dev.62 must deploy dev.62 to the fixed path")
	}
	// 回復是在兩版之間切換：再回復一次回到 dev.63。回到 dev.61 移除固定檔見 TestWindowsX86DLLFollowsUpgradeRollbackAndUninstall。
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	loaded.load(t, FixedDLL(base)) // 解除安裝時仍有應用程式載著
	os.WriteFile(FixedDLL(base)+".new", []byte("stale"), 0600)
	if _, err = UninstallWindows(base, r); err != nil {
		t.Fatal(err)
	}
	if r.x64 != "" || r.x86 != "" {
		t.Fatalf("uninstall must clear both views: %+v", r)
	}
	got := fixedEntries(t, base)
	if len(got) != 1 || !strings.HasPrefix(got[0], "0c_tsf.dll.old-") {
		t.Fatalf("uninstall must remove fixed files and keep only the loaded one: %v", got)
	}
	// 解除安裝後 repair 只清殘留，不重新部署。
	clear(loaded)
	if _, err = RepairFixed(base); err != nil {
		t.Fatal(err)
	}
	if got := fixedEntries(t, base); len(got) != 0 {
		t.Fatalf("repair after uninstall must only clean up: %v", got)
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.x64 != FixedDLL(base) {
		t.Fatalf("undoing the uninstall must redeploy the fixed path: %+v", r)
	}
}

// 同一版重新安裝（例如安裝精靈再跑一次）：固定檔被刪掉時補回；舊版更新器註冊在版本目錄時改指固定路徑。
func TestFixedReinstallSameVersion(t *testing.T) {
	base := t.TempDir()
	fakeFixedIO(t, loadedFiles{}, nil)
	r := &viewRegistrar{}
	bundle := x86Fixture(t, "0.1.0-dev.62")
	dir, _, err := InstallWindows(bundle, base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(FixedDLL(base))
	if _, _, err = InstallWindows(bundle, base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(dir, "0c_tsf.dll")) {
		t.Fatal("reinstall must redeploy the fixed DLL")
	}
	// 舊版更新器（dev.61）安裝 dev.62 時註冊的是版本目錄。
	r.x64 = filepath.Join(dir, "0c_tsf.dll")
	if _, _, err = InstallWindows(bundle, base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if r.x64 != FixedDLL(base) {
		t.Fatalf("reinstall must move the registration to the fixed path: %+v", r)
	}
}
