package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// x86Fixture 是帶 32 位元 TSF DLL（x86/0c_tsf.dll，#112）的套件；0.1.0-dev.62 起的 Windows 套件都有它。
func x86Fixture(t *testing.T, version string) string {
	t.Helper()
	root := bundleFixture(t, version)
	r, err := Verify(root, "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, filepath.FromSlash(WindowsDLL32))
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte(version+" i686 dll"), 0600); err != nil {
		t.Fatal(err)
	}
	r.Files[WindowsDLL32], _ = Hash(p)
	if err = writeJSON(filepath.Join(root, "release.json"), r); err != nil {
		t.Fatal(err)
	}
	return root
}

// viewRegistrar 模擬兩個登錄視圖（platforms/windows/tsf/src/register.rs 與 cmd/0c-update 的 registrar）：
// 對 x64 DLL 註冊時，旁邊有 x86/0c_tsf.dll 就同時寫 32 位元視圖，沒有就清掉 32 位元視圖；取消註冊時兩個都清。
type viewRegistrar struct {
	x64, x86 string
}

func (r *viewRegistrar) Current() (string, error) { return r.x64, nil }
func (r *viewRegistrar) Register(dll string) error {
	r.x64, r.x86 = dll, ""
	if _, err := os.Stat(WindowsDLL32Of(dll)); err == nil {
		r.x86 = WindowsDLL32Of(dll)
	}
	return nil
}
func (r *viewRegistrar) Unregister(string) error {
	r.x64, r.x86 = "", ""
	return nil
}

func TestWindowsDLL32Path(t *testing.T) {
	dir := filepath.Join("C:", "Zuin", "versions", "0.1.0-dev.62-abc")
	if got, want := WindowsDLL32Of(filepath.Join(dir, "0c_tsf.dll")), filepath.Join(dir, "x86", "0c_tsf.dll"); got != want {
		t.Fatalf("WindowsDLL32Of = %s, want %s", got, want)
	}
}

func TestWindowsBundleWithX86DLLVerifies(t *testing.T) {
	root := x86Fixture(t, "0.1.0-dev.62")
	if _, err := Verify(root, "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "x86", "0c_tsf.dll"), []byte("changed"), 0600)
	if _, err := Verify(root, "windows", "amd64"); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("tampered 32-bit DLL accepted: %v", err)
	}
	// 沒列在 release.json 的 x86 目錄檔案（例如手動放進去的 DLL）同樣拒絕。
	plain := bundleFixture(t, "0.1.0-dev.61")
	os.MkdirAll(filepath.Join(plain, "x86"), 0700)
	os.WriteFile(filepath.Join(plain, "x86", "0c_tsf.dll"), []byte("unlisted"), 0600)
	if _, err := Verify(plain, "windows", "amd64"); err == nil || !strings.Contains(err.Error(), "unlisted") {
		t.Fatalf("unlisted 32-bit DLL accepted: %v", err)
	}
}

// 從沒有 32 位元 DLL 的舊版升級、回復、再升級與解除安裝，兩個視圖都跟著版本走（#112）。
func TestWindowsX86DLLFollowsUpgradeRollbackAndUninstall(t *testing.T) {
	base := t.TempDir()
	r := &viewRegistrar{}
	old, _, err := InstallWindows(bundleFixture(t, "0.1.0-dev.61"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	if r.x86 != "" {
		t.Fatalf("old version has no 32-bit DLL but registered %s", r.x86)
	}
	next, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	dll32 := filepath.Join(next, "x86", "0c_tsf.dll")
	// dev.62 起註冊固定路徑（#121），32 位元在 tsf\x86。
	fixed32 := WindowsDLL32Of(FixedDLL(base))
	if r.x64 != FixedDLL(base) || r.x86 != fixed32 {
		t.Fatalf("upgrade must register both views: %+v", r)
	}
	receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if err != nil || receipt.Files[WindowsDLL32] == "" {
		t.Fatalf("receipt must record the 32-bit DLL: %+v %v", receipt, err)
	}
	// 收據核對涵蓋 32 位元 DLL：被改動時回復拒絕（不切換註冊），解除安裝照常進行並回報。
	if err = os.WriteFile(dll32, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RollbackWindows(base, r, false); err == nil || !strings.Contains(err.Error(), WindowsDLL32) {
		t.Fatalf("rollback over a changed 32-bit DLL: %v", err)
	}
	if r.x86 != fixed32 {
		t.Fatal("refused rollback changed registration")
	}
	if err = os.WriteFile(dll32, []byte("0.1.0-dev.62 i686 dll"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatal(err)
	}
	if r.x64 != filepath.Join(old, "0c_tsf.dll") || r.x86 != "" {
		t.Fatalf("rollback to a version without the 32-bit DLL must clear the 32-bit view: %+v", r)
	}
	if _, err = os.Stat(fixed32); !os.IsNotExist(err) {
		t.Fatalf("rollback to dev.61 must remove the fixed DLLs: %v", err)
	}
	if _, _, err = InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if r.x86 != fixed32 {
		t.Fatalf("reinstall must register the 32-bit view again: %+v", r)
	}
	os.Remove(dll32) // 例如被防毒軟體隔離
	changed, err := UninstallWindows(base, r)
	if err != nil {
		t.Fatal(err)
	}
	if changed == nil || !strings.Contains(filepath.ToSlash(changed.Error()), WindowsDLL32) {
		t.Fatalf("missing 32-bit DLL must be reported: %v", changed)
	}
	if r.x64 != "" || r.x86 != "" {
		t.Fatalf("uninstall must clear both views: %+v", r)
	}
}
