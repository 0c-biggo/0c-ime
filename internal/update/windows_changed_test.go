package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #254：被換下的版本只是回復用。它的檔案被改動、缺少（防毒軟體隔離或修改、使用者自己改過 0c-server.exe）時，
// 新版照常安裝；只是之後回復不到那個被改動的版本。新版本身照樣逐檔核對。

// mutateVersion 改動版本目錄裡的一個檔案：changed 改內容、missing 刪掉。
func mutateVersion(t *testing.T, dir, how, name string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	var err error
	switch how {
	case "changed":
		err = os.WriteFile(p, []byte("patched by someone else"), 0644)
	case "missing":
		err = os.Remove(p)
	default:
		t.Fatalf("unknown mutation %s", how)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestWindowsInstallProceedsWhenPreviousVersionChanged(t *testing.T) {
	for _, c := range []struct{ how, name string }{
		{"changed", "0c-server.exe"}, // 客戶回報的情況
		{"missing", "0c-ui.exe"},
		{"changed", "0c_tsf.dll"}, // 版本目錄的正本；固定路徑那份沒動
		{"missing", "x86/0c_tsf.dll"},
		{"changed", "data/lm.bin"},
	} {
		t.Run(c.how+" "+c.name, func(t *testing.T) {
			base := t.TempDir()
			user := filepath.Join(base, UserStore)
			os.WriteFile(user, []byte("private learning"), 0600)
			r := &viewRegistrar{}
			old, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r)
			if err != nil {
				t.Fatal(err)
			}
			mutateVersion(t, old, c.how, c.name)
			next, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.63"), base, "amd64", r)
			if err != nil {
				t.Fatalf("a changed previous version must not block installing the new one: %v", err)
			}
			receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
			if err != nil || receipt.Version != "0.1.0-dev.63" || !receipt.Active {
				t.Fatalf("receipt must name the new version: %+v %v", receipt, err)
			}
			if r.x64 != FixedDLL(base) || readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(next, "0c_tsf.dll")) ||
				readFile(t, WindowsDLL32Of(FixedDLL(base))) != readFile(t, filepath.Join(next, "x86", "0c_tsf.dll")) {
				t.Fatalf("the new version's DLLs must be deployed and registered: %+v", r)
			}
			if readFile(t, user) != "private learning" {
				t.Fatal("personal dictionary changed")
			}
			if receipt.Previous == "" || readFile(t, filepath.Join(base, filepath.FromSlash(receipt.Previous), UserStore)) != "private learning" {
				t.Fatal("the personal dictionary must still be backed up before switching")
			}
			// 回復目標仍要完好：被改動的舊版拒絕回復，註冊與收據都不動。
			before := readFile(t, filepath.Join(base, "install-receipt.json"))
			if _, err = RollbackWindows(base, r, false); err == nil || !strings.Contains(err.Error(), filepath.Base(c.name)) {
				t.Fatalf("rollback to the changed version must be refused and name the file: %v", err)
			}
			if r.x64 != FixedDLL(base) || readFile(t, filepath.Join(base, "install-receipt.json")) != before ||
				readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(next, "0c_tsf.dll")) {
				t.Fatal("refused rollback changed registration, receipt or fixed DLL")
			}
		})
	}
}

// 同一版重新安裝（例如重新執行安裝精靈）：版本目錄被改動時移開重放，改動的檔案回到收據的內容。
func TestWindowsReinstallSameVersionRepairsChangedFile(t *testing.T) {
	base := t.TempDir()
	r := &viewRegistrar{}
	bundle := x86Fixture(t, "0.1.0-dev.62")
	dir, _, err := InstallWindows(bundle, base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	mutateVersion(t, dir, "changed", "0c-server.exe")
	again, _, err := InstallWindows(bundle, base, "amd64", r)
	if err != nil {
		t.Fatalf("reinstalling the same version must repair it: %v", err)
	}
	receipt, err := readReceipt(filepath.Join(base, "install-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if again != dir || verifyReceipt(base, receipt) != nil {
		t.Fatalf("the version directory must match its receipt again: %v", verifyReceipt(base, receipt))
	}
	if r.x64 != FixedDLL(base) {
		t.Fatalf("registration must stay on the fixed path: %+v", r)
	}
}

// 回復（只有命令列）照舊要求目前版本完好；重新執行同一版的安裝修好之後就能回復。
func TestWindowsRollbackAfterRepairingChangedCurrentVersion(t *testing.T) {
	base := t.TempDir()
	r := &viewRegistrar{}
	v62, _, err := InstallWindows(x86Fixture(t, "0.1.0-dev.62"), base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	bundle := x86Fixture(t, "0.1.0-dev.63")
	v63, _, err := InstallWindows(bundle, base, "amd64", r)
	if err != nil {
		t.Fatal(err)
	}
	mutateVersion(t, v63, "changed", "0c-server.exe")
	if _, err = RollbackWindows(base, r, false); err == nil || !strings.Contains(err.Error(), "0c-server.exe") {
		t.Fatalf("rollback over a changed current version: %v", err)
	}
	if _, _, err = InstallWindows(bundle, base, "amd64", r); err != nil {
		t.Fatal(err)
	}
	if _, err = RollbackWindows(base, r, false); err != nil {
		t.Fatalf("rollback after repairing the current version: %v", err)
	}
	if r.x64 != FixedDLL(base) || readFile(t, FixedDLL(base)) != readFile(t, filepath.Join(v62, "0c_tsf.dll")) {
		t.Fatal("the rolled back version's DLL must be deployed")
	}
}
