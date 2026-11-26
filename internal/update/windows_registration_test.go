package update

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 註冊不變的外殼更新（#294）：指紋相同時只以一般權限換固定路徑的 DLL，不呼叫 Register／Unregister。

const (
	fingerprintA = "1111111111111111111111111111111111111111111111111111111111111111"
	fingerprintB = "2222222222222222222222222222222222222222222222222222222222222222"
)

// fingerprinted 是帶 32 位元 DLL 與註冊指紋的套件；fp 為空字串時是還沒有指紋的舊套件。
func fingerprinted(t *testing.T, version, fp string) string {
	t.Helper()
	root := x86Fixture(t, version)
	r, err := Verify(root, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	r.TSFRegistration = fp
	if err = writeJSON(filepath.Join(root, "release.json"), r); err != nil {
		t.Fatal(err)
	}
	return root
}

// keepRegistrar 在 viewRegistrar 上加語言 profile 與呼叫次數：Register 寫 profile，Unregister 清掉。
type keepRegistrar struct {
	viewRegistrar
	profile                bool
	registers, unregisters int
}

func (r *keepRegistrar) Register(dll string) error {
	r.registers++
	r.profile = true
	return r.viewRegistrar.Register(dll)
}
func (r *keepRegistrar) Unregister(dll string) error {
	r.unregisters++
	r.profile = false
	return r.viewRegistrar.Unregister(dll)
}
func (r *keepRegistrar) RegistrationIntact(base string, x86 bool) error {
	want := ""
	if x86 {
		want = WindowsDLL32Of(FixedDLL(base))
	}
	if r.x86 != want {
		return fmt.Errorf("32-bit view is %q", r.x86)
	}
	if !r.profile {
		return errors.New("language profile missing")
	}
	return nil
}

type probeCall struct{ x64, x86 string }

func TestKeepingRegistrationSwapsOnlyTheFixedDLLs(t *testing.T) {
	base := t.TempDir()
	loaded := loadedFiles{}
	fakeFixedIO(t, loaded, nil)
	r := &keepRegistrar{}
	a, _, err := InstallWindows(fingerprinted(t, "0.2.15", fingerprintA), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if r.registers != 1 {
		t.Fatalf("first install registers once: %+v", r)
	}
	loaded.load(t, FixedDLL(base)) // 應用程式仍載著舊版
	bundle := fingerprinted(t, "0.2.16", fingerprintA)
	release, err := Verify(bundle, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if err = WindowsKeepsRegistration(base, r, release); err != nil {
		t.Fatalf("same fingerprint must not need administrator: %v", err)
	}
	var probes []probeCall
	b, _, err := InstallWindowsKeepingRegistration(bundle, base, "amd64", r, func(x64, x86 string) error {
		probes = append(probes, probeCall{x64, x86})
		if readFile(t, x64) != readFile(t, filepath.Join(bundle, "0c_tsf.dll")) {
			t.Errorf("probe ran before the new DLL was in place")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.registers != 1 || r.unregisters != 0 || r.x64 != FixedDLL(base) || r.x86 != WindowsDLL32Of(FixedDLL(base)) {
		t.Fatalf("registration must not be touched: %+v", r)
	}
	if len(probes) != 1 || probes[0] != (probeCall{FixedDLL(base), WindowsDLL32Of(FixedDLL(base))}) {
		t.Fatalf("probe calls: %+v", probes)
	}
	if readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(b, "0c_tsf.dll")) ||
		readFile(t, WindowsDLL32Of(FixedDLL(base))) != readFile(t, filepath.Join(b, "x86", "0c_tsf.dll")) {
		t.Fatal("fixed DLLs must be the new version")
	}
	receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if err != nil || receipt.Version != "0.2.16" || !strings.HasPrefix(receipt.Previous, "backups/windows-") {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	// 載著舊檔的應用程式不受影響：舊檔改名留著，下一次修復時才清。
	if _, err = os.Stat(FixedDLL(base) + ".old-" + filepath.Base(a)); err != nil {
		t.Fatalf("loaded old DLL must be kept: %v", err)
	}
	// 之後再一次同指紋的更新仍不必提權；回復（系統管理員）照舊重新註冊上一版。
	if err = WindowsKeepsRegistration(base, r, &Release{Version: "0.2.17", Files: release.Files, TSFRegistration: fingerprintA}); err != nil {
		t.Fatal(err)
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.registers != 2 || readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(a, "0c_tsf.dll")) {
		t.Fatalf("rollback re-registers the previous version: %+v", r)
	}
}

func TestKeepingRegistrationRefusesWhenTheRegistrationMayChange(t *testing.T) {
	cases := []struct {
		name      string
		installed string // 已安裝版本的指紋
		next      string // 新版的指紋
		old       bool   // 已安裝的是不從固定路徑載入的 dev.61
		setup     func(t *testing.T, base string, r *keepRegistrar) Registrar
	}{
		{name: "next has no fingerprint", installed: fingerprintA},
		{name: "installed has no fingerprint", next: fingerprintA},
		{name: "fingerprint differs", installed: fingerprintA, next: fingerprintB},
		{name: "version directory before the fixed path", next: fingerprintA, old: true},
		{name: "registry points elsewhere", installed: fingerprintA, next: fingerprintA, setup: func(t *testing.T, base string, r *keepRegistrar) Registrar {
			r.x64 = filepath.Join(base, "versions", "other", "0c_tsf.dll")
			return r
		}},
		{name: "language profile missing", installed: fingerprintA, next: fingerprintA, setup: func(t *testing.T, base string, r *keepRegistrar) Registrar {
			r.profile = false
			return r
		}},
		{name: "32-bit view missing", installed: fingerprintA, next: fingerprintA, setup: func(t *testing.T, base string, r *keepRegistrar) Registrar {
			r.x86 = ""
			return r
		}},
		{name: "registrar cannot verify", installed: fingerprintA, next: fingerprintA, setup: func(t *testing.T, base string, r *keepRegistrar) Registrar {
			return &r.viewRegistrar
		}},
		{name: "installed release.json does not match the receipt", installed: fingerprintA, next: fingerprintA, setup: func(t *testing.T, base string, r *keepRegistrar) Registrar {
			receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(base, filepath.FromSlash(receipt.Directory), "release.json")
			release, err := Verify(filepath.Dir(p), "windows", "amd64")
			if err != nil {
				t.Fatal(err)
			}
			release.Version = "0.2.99"
			if err = writeJSON(p, release); err != nil {
				t.Fatal(err)
			}
			return r
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			r := &keepRegistrar{}
			first := fingerprinted(t, "0.2.15", c.installed)
			if c.old {
				first = bundleFixture(t, "0.1.0-dev.61")
			}
			if _, _, err := InstallWindows(first, base, "amd64", r); err != nil {
				t.Fatal(err)
			}
			var reg Registrar = r
			if c.setup != nil {
				reg = c.setup(t, base, r)
			}
			receipt := readFile(t, filepath.Join(base, "install-receipt.json"))
			before := fixedState(base)
			versions, _ := os.ReadDir(filepath.Join(base, "versions"))
			state := *r
			bundle := fingerprinted(t, "0.2.16", c.next)
			release, err := Verify(bundle, "windows", "amd64")
			if err != nil {
				t.Fatal(err)
			}
			if err := WindowsKeepsRegistration(base, reg, release); !errors.Is(err, ErrRegistrationChanged) {
				t.Fatalf("pre-check must ask for administrator: %v", err)
			}
			probed := false
			_, _, err = InstallWindowsKeepingRegistration(bundle, base, "amd64", reg, func(string, string) error { probed = true; return nil })
			if !errors.Is(err, ErrRegistrationChanged) {
				t.Fatalf("install must refuse: %v", err)
			}
			after, _ := os.ReadDir(filepath.Join(base, "versions"))
			if probed || readFile(t, filepath.Join(base, "install-receipt.json")) != receipt || fixedState(base) != before ||
				len(after) != len(versions) || *r != state {
				t.Fatalf("a refused install must not change anything: %+v", r)
			}
		})
	}
}

// 換上的 DLL 載入不了：換回原本的版本，收據與註冊都不動，下一次修復也不會再換成新版。
func TestKeepingRegistrationRestoresThePreviousDLLWhenTheNewOneDoesNotLoad(t *testing.T) {
	base := t.TempDir()
	loaded := loadedFiles{}
	fakeFixedIO(t, loaded, nil)
	r := &keepRegistrar{}
	a, _, err := InstallWindows(fingerprinted(t, "0.2.15", fingerprintA), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	loaded.load(t, FixedDLL(base))
	receipt := readFile(t, filepath.Join(base, "install-receipt.json"))
	_, _, err = InstallWindowsKeepingRegistration(fingerprinted(t, "0.2.16", fingerprintA), base, "amd64", r, func(x64, x86 string) error {
		return errors.New("LoadLibrary: %1 is not a valid Win32 application")
	})
	if !errors.Is(err, ErrDLLProbe) {
		t.Fatalf("probe failure must be reported as ErrDLLProbe: %v", err)
	}
	if readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(a, "0c_tsf.dll")) ||
		readFile(t, WindowsDLL32Of(FixedDLL(base))) != readFile(t, filepath.Join(a, "x86", "0c_tsf.dll")) {
		t.Fatal("the previous DLLs must be back at the fixed path")
	}
	if readFile(t, filepath.Join(base, "install-receipt.json")) != receipt || r.registers != 1 || r.unregisters != 0 {
		t.Fatalf("receipt and registration must stay: %+v", r)
	}
	if changed, err := RepairFixed(base); err != nil || changed {
		t.Fatalf("repair after the restore: changed=%v %v", changed, err)
	}
}

func TestVerifyRejectsMalformedRegistrationFingerprint(t *testing.T) {
	root := fingerprinted(t, "0.2.16", "NOT-A-HASH")
	if _, err := Verify(root, "windows", "amd64"); err == nil || !strings.Contains(err.Error(), "tsf_registration") {
		t.Fatalf("malformed fingerprint accepted: %v", err)
	}
	if r, err := Verify(fingerprinted(t, "0.2.16", fingerprintA), "windows", "amd64"); err != nil || r.TSFRegistration != fingerprintA {
		t.Fatalf("fingerprint must round-trip: %+v %v", r, err)
	}
}

func zipBundle(t *testing.T, root, name string) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "0c.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out, err := w.Create(name + "/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = out.Write(b)
		return err
	})
	if err == nil {
		err = w.Close()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestPeekReleaseReadsOnlyTheManifest(t *testing.T) {
	archive := zipBundle(t, fingerprinted(t, "0.2.16", fingerprintA), "0c-0.2.16-windows-amd64")
	r, err := PeekRelease(archive)
	if err != nil || r.Version != "0.2.16" || r.TSFRegistration != fingerprintA || r.Files[WindowsDLL32] == "" {
		t.Fatalf("PeekRelease = %+v, %v", r, err)
	}
	// 沒有根目錄的 release.json（或在更深的子目錄）不算。
	if _, err = PeekRelease(zipBundle(t, t.TempDir(), "empty")); err == nil {
		t.Fatal("archive without release.json accepted")
	}
	if _, err = PeekRelease(filepath.Join(t.TempDir(), "0c.tar.gz")); err == nil {
		t.Fatal("only .zip bundles are peeked")
	}
}
