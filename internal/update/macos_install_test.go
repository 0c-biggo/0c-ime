package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/userlock"
)

func macBundle(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	r := Release{Schema: 1, Product: "0c", Version: version, Channel: "internal-preview", OS: "darwin", Arch: "arm64", Files: map[string]string{}}
	names := []string{"0c-update", MacApp + "/Contents/Info.plist", MacApp + "/Contents/MacOS/0c", MacApp + "/Contents/MacOS/0c-server",
		MacApp + "/Contents/Resources/neural/select/model.onnx"}
	for _, n := range []string{"manifest.json", "lexicon.bin", "lm.bin", "NOTICE.txt", "report.txt"} {
		names = append(names, MacApp+"/Contents/Resources/data/"+n)
	}
	for _, name := range names {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(version+" "+name), 0o600)
		r.Files[name], _ = Hash(p)
	}
	if err := writeJSON(filepath.Join(root, "release.json"), r); err != nil {
		t.Fatal(err)
	}
	return root
}

func appVersion(t *testing.T, p MacPaths) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(p.app(), "Contents/MacOS/0c-server"))
	if err != nil {
		return "absent"
	}
	return string(b)
}

func TestMacInstallUpgradeRollbackUninstall(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/Zuin")}
	// 0c 以前的原型 App：沒有收據，升級後可回復。
	prototype := filepath.Join(p.appNamed(LegacyMacApp), "Contents/MacOS/zuin-server")
	os.MkdirAll(filepath.Dir(prototype), 0o755)
	os.WriteFile(prototype, []byte("prototype"), 0o755)
	os.MkdirAll(p.Support, 0o700)
	os.WriteFile(filepath.Join(p.Support, "user.json"), []byte("private learning"), 0o600)

	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.17"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if v := appVersion(t, p); v != "0.1.0-dev.17 "+MacApp+"/Contents/MacOS/0c-server" {
		t.Fatalf("installed %q", v)
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(prototype); string(b) != "prototype" {
		t.Fatal("prototype not restored")
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if appVersion(t, p) == "absent" {
		t.Fatal("cannot return to 0.1.0-dev.17")
	}
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.18"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if v := appVersion(t, p); v != "0.1.0-dev.17 "+MacApp+"/Contents/MacOS/0c-server" {
		t.Fatalf("rollback got %q", v)
	}
	if _, err := UninstallMac(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.app()); !os.IsNotExist(err) {
		t.Fatal("uninstall left the app installed")
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if appVersion(t, p) == "absent" {
		t.Fatal("cannot undo uninstall")
	}
	if b, _ := os.ReadFile(filepath.Join(p.Support, "user.json")); string(b) != "private learning" {
		t.Fatal("changed personal dictionary")
	}
}

func TestMacReceiptPreservesInstallSessionOnRollback(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "Support")}
	for _, version := range []string{"0.1.0-dev.17", "0.1.0-dev.18"} {
		p.SessionASID++
		if _, _, err := InstallMac(macBundle(t, version), "arm64", p); err != nil {
			t.Fatal(err)
		}
		r, err := readMacReceipt(p.receipt())
		if err != nil || r.SessionASID != p.SessionASID {
			t.Fatalf("receipt %+v %v", r, err)
		}
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	r, err := readMacReceipt(p.receipt())
	if err != nil || r.Version != "0.1.0-dev.17" || r.SessionASID != 1 {
		t.Fatalf("rollback boundary %+v %v", r, err)
	}
	// A receipt written by an old updater remains readable with an unknown boundary.
	if err := writeJSON(p.receipt(), &MacReceipt{Schema: 1, Product: "0c", Version: "old", Active: true}); err != nil {
		t.Fatal(err)
	}
	r, err = readMacReceipt(p.receipt())
	if err != nil || r.SessionASID != 0 {
		t.Fatalf("old receipt %+v %v", r, err)
	}
}

func TestMacRefusesWrongPlatform(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "Zuin")}
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.17"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.18"), "amd64", p); err == nil {
		t.Fatal("accepted wrong architecture")
	}
	extra := macBundle(t, "0.1.0-dev.19")
	os.WriteFile(filepath.Join(extra, MacApp, "Contents/MacOS/extra"), []byte("x"), 0o755)
	if _, err := Verify(extra, "darwin", "arm64"); err == nil {
		t.Fatal("accepted unlisted app file")
	}
}

// 0.1.0-dev.21 以前的安裝：Zuin.app 與沒有 app 欄位的收據。升級後只剩 0c.app，回復時 Zuin.app 照原名放回。
func TestMacUpgradeFromZuinAppAndBack(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	legacy := p.appNamed(LegacyMacApp)
	files := map[string]string{}
	for _, name := range []string{"Contents/MacOS/Zuin", "Contents/MacOS/0c-server", "Contents/Info.plist"} {
		f := filepath.Join(legacy, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte("dev.21 "+name), 0o755)
		files[name], _ = Hash(f)
	}
	os.MkdirAll(p.Support, 0o700)
	writeJSON(p.receipt(), &MacReceipt{Schema: 1, Product: "0c", Version: "0.1.0-dev.21", Files: files, Active: true})

	installed, _, err := InstallMac(macBundle(t, "0.1.0-dev.22"), "arm64", p)
	if err != nil {
		t.Fatal(err)
	}
	if installed != p.app() {
		t.Fatalf("installed at %s", installed)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("Zuin.app left next to 0c.app")
	}
	r, err := readMacReceipt(p.receipt())
	if err != nil || r.App != MacApp || r.Version != "0.1.0-dev.22" {
		t.Fatalf("receipt %+v %v", r, err)
	}

	back, err := RollbackMac(p)
	if err != nil {
		t.Fatal(err)
	}
	if back.App != legacy {
		t.Fatalf("rollback restored %q", back.App)
	}
	if b, _ := os.ReadFile(filepath.Join(legacy, "Contents/MacOS/Zuin")); string(b) != "dev.21 Contents/MacOS/Zuin" {
		t.Fatal("Zuin.app not restored")
	}
	if _, err := os.Stat(p.app()); !os.IsNotExist(err) {
		t.Fatal("0c.app left after rollback")
	}
	if _, err := os.Stat(filepath.Join(back.Backup, MacApp, "Contents/MacOS/0c")); err != nil {
		t.Fatal("0c.app not kept in the rollback backup")
	}
	if r, _ := readMacReceipt(p.receipt()); r == nil || r.appName() != LegacyMacApp || !r.Active {
		t.Fatalf("legacy receipt not restored: %+v", r)
	}

	// 回復後的 Zuin.app 換成別的 App：拒絕覆寫；舊 bundle ID 的 Zuin.app 只是檔案被改動時照常認得。
	plist := filepath.Join(legacy, "Contents/Info.plist")
	os.WriteFile(plist, plistWithID("com.example.other"), 0o644)
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.23"), "arm64", p); err == nil {
		t.Fatal("overwrote an app that is not ours")
	}
	os.WriteFile(plist, plistWithID("org.zuin.inputmethod.Zuin"), 0o644)
	if changed, err := CheckMac(p); err != nil || changed == nil || changed.Error() != "Zuin.app 與安裝收據不符：已改動 Contents/Info.plist" {
		t.Fatalf("legacy check %v %v", changed, err)
	}
	os.WriteFile(plist, []byte("dev.21 Contents/Info.plist"), 0o755)

	again, err := RollbackMac(p)
	if err != nil {
		t.Fatal(err)
	}
	if again.App != p.app() || appVersion(t, p) != "0.1.0-dev.22 "+MacApp+"/Contents/MacOS/0c-server" {
		t.Fatalf("undo rollback got %+v %q", again, appVersion(t, p))
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("Zuin.app left after undoing the rollback")
	}
}

func TestMigrateMacSupport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support")
	legacy, current := filepath.Join(dir, "Zuin"), filepath.Join(dir, "0c")
	if moved, err := MigrateMacSupport(legacy, current); moved || err != nil {
		t.Fatalf("nothing to migrate: %v %v", moved, err)
	}
	os.MkdirAll(filepath.Join(legacy, "bin"), 0o700)
	os.WriteFile(filepath.Join(legacy, "user.json"), []byte("private learning"), 0o600)
	os.MkdirAll(current, 0o700) // 新 App 先啟動時建立的空目錄
	moved, err := MigrateMacSupport(legacy, current)
	if !moved || err != nil {
		t.Fatalf("migrate: %v %v", moved, err)
	}
	if b, _ := os.ReadFile(filepath.Join(current, "user.json")); string(b) != "private learning" {
		t.Fatal("personal dictionary not moved")
	}
	if b, _ := os.ReadFile(filepath.Join(legacy, "user.json")); string(b) != "private learning" {
		t.Fatal("legacy path does not reach the moved data")
	}
	if target, _ := os.Readlink(legacy); target != "0c" {
		t.Fatalf("link target %q", target)
	}
	if moved, err := MigrateMacSupport(legacy, current); moved || err != nil {
		t.Fatalf("second run: %v %v", moved, err)
	}

	other := filepath.Join(t.TempDir(), "Application Support")
	os.MkdirAll(filepath.Join(other, "Zuin"), 0o700)
	os.WriteFile(filepath.Join(other, "Zuin", "user.json"), []byte("a"), 0o600)
	os.MkdirAll(filepath.Join(other, "0c"), 0o700)
	os.WriteFile(filepath.Join(other, "0c", "user.json"), []byte("b"), 0o600)
	if _, err := MigrateMacSupport(filepath.Join(other, "Zuin"), filepath.Join(other, "0c")); err == nil {
		t.Fatal("merged two personal data directories")
	}
}

// 新引擎啟動時會在支援目錄留下寫入者鎖檔（#120）；目錄裡只有沒人持有的鎖檔仍算空目錄，
// 照常搬移舊資料。鎖被持有表示新引擎正在使用這個目錄，不能把舊目錄搬到它底下。
func TestMigrateMacSupportIgnoresIdleWriterLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support")
	legacy, current := filepath.Join(dir, "Zuin"), filepath.Join(dir, "0c")
	os.MkdirAll(legacy, 0o700)
	os.WriteFile(filepath.Join(legacy, "user.json"), []byte("private learning"), 0o600)
	os.MkdirAll(current, 0o700)
	held, err := userlock.TryLock(current)
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := MigrateMacSupport(legacy, current); moved || err == nil {
		t.Fatalf("an engine holds the new directory; migration must wait: %v %v", moved, err)
	}
	held.Release()
	if moved, err := MigrateMacSupport(legacy, current); !moved || err != nil {
		t.Fatalf("an idle lock file must not block migration: %v %v", moved, err)
	}
	if b, _ := os.ReadFile(filepath.Join(current, "user.json")); string(b) != "private learning" {
		t.Fatal("personal dictionary not moved")
	}
}

// MacRollbackTarget 在搬移之前指出 RollbackMac 會放回哪一個 App（判斷它有沒有內附排程）。
func TestMacRollbackTarget(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	if _, err := MacRollbackTarget(p); err == nil {
		t.Fatal("沒有安裝時不應有回復目標")
	}
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.38"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.39"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	target, err := MacRollbackTarget(p)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "Contents/MacOS/0c-server")); !strings.HasPrefix(string(b), "0.1.0-dev.38") {
		t.Fatalf("回復目標不是上一版：%s %q", target, b)
	}
	r, err := RollbackMac(p)
	if err != nil {
		t.Fatal(err)
	}
	if appVersion(t, p) != "0.1.0-dev.38 "+MacApp+"/Contents/MacOS/0c-server" || r.App != p.app() {
		t.Fatalf("回復結果與目標不符：%q", appVersion(t, p))
	}
}

// #205：平台為了背景項目把備份裡的檔案改了名，回復時在核對之前（持有安裝鎖）先改回，放回的 App 與收據一致。
func TestMacRollbackPreparesTargetUnderLock(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	for _, v := range []string{"0.1.0-dev.38", "0.1.0-dev.39"} {
		if _, _, err := InstallMac(macBundle(t, v), "arm64", p); err != nil {
			t.Fatal(err)
		}
	}
	target, err := MacRollbackTarget(p)
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(target, "Contents/MacOS/0c-server")
	if err := os.Rename(server, server+".inactive"); err != nil {
		t.Fatal(err)
	}
	var prepared []string
	p.PrepareRollback = func(app string) {
		if _, err := os.Stat(filepath.Join(p.Support, "update.lock")); err != nil {
			t.Errorf("PrepareRollback must run while holding the install lock: %v", err)
		}
		prepared = append(prepared, app)
		os.Rename(filepath.Join(app, "Contents/MacOS/0c-server.inactive"), filepath.Join(app, "Contents/MacOS/0c-server"))
	}
	r, err := RollbackMac(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) == 0 || prepared[0] != target {
		t.Fatalf("PrepareRollback not called with the target: %v", prepared)
	}
	if r.Restored != nil {
		t.Fatalf("restored app must match its receipt after PrepareRollback: %v", r.Restored)
	}
	if appVersion(t, p) != "0.1.0-dev.38 "+MacApp+"/Contents/MacOS/0c-server" {
		t.Fatalf("rolled back to %q", appVersion(t, p))
	}
}

func plistWithID(id string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
    <key>CFBundleIdentifier</key><string>` + id + `</string>
</dict></plist>
`)
}

// 已安裝的 App 被改動（#83：改過 0c-server 並重新簽章、防毒軟體動過檔案）：確認是自己的安裝就照常更新，
// 整個 App 換新、舊的移入備份並列出不符之處；不是靈犀注音的 App、連結或身分無法確認時仍拒絕，原 App 不動。
func TestMacInstallOverModifiedApp(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere")
	os.WriteFile(outside, []byte("not ours"), 0o644)
	for _, tc := range []struct {
		name   string
		modify func(app string)
		want   string // changed 應包含的字串
		refuse bool
	}{
		{"changed", func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/MacOS/0c-server"), []byte("patched and re-signed"), 0o755)
		}, "已改動 Contents/MacOS/0c-server", false},
		{"missing", func(app string) {
			os.Remove(filepath.Join(app, "Contents/Resources/neural/select/model.onnx"))
		}, "缺少 Contents/Resources/neural/select/model.onnx", false},
		{"extra", func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/.DS_Store"), []byte("x"), 0o644)
		}, "多出 Contents/.DS_Store", false},
		{"file becomes link", func(app string) {
			f := filepath.Join(app, "Contents/MacOS/0c-server")
			os.Remove(f)
			os.Symlink(outside, f)
		}, "不是一般檔案 Contents/MacOS/0c-server", false},
		{"directory becomes link", func(app string) {
			d := filepath.Join(app, "Contents/Resources/neural")
			os.RemoveAll(d)
			os.Symlink(filepath.Dir(outside), d)
		}, "不是一般檔案 Contents/Resources/neural、缺少 Contents/Resources/neural/select/model.onnx", false},
		{"same bundle ID, edited Info.plist", func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/Info.plist"), plistWithID("com.biggo.inputmethod.0c"), 0o644)
		}, "已改動 Contents/Info.plist", false},
		{"whole app missing", func(app string) { os.RemoveAll(app) }, MacApp + " 不存在", false},
		{"different bundle ID", func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/Info.plist"), plistWithID("com.example.other"), 0o644)
		}, "", true},
		{"Info.plist missing", func(app string) { os.Remove(filepath.Join(app, "Contents/Info.plist")) }, "", true},
		{"Info.plist unreadable format", func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/Info.plist"), []byte("bplist00\x00binary"), 0o644)
		}, "", true},
		{"app is a link", func(app string) {
			moved := filepath.Join(filepath.Dir(filepath.Dir(app)), "real.app")
			os.Rename(app, moved)
			os.Symlink(moved, app)
		}, "", true},
		{"app is a file", func(app string) {
			os.RemoveAll(app)
			os.WriteFile(app, []byte("x"), 0o644)
		}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			p := MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "0c")}
			if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.17"), "arm64", p); err != nil {
				t.Fatal(err)
			}
			tc.modify(p.app())
			before, _ := os.ReadFile(p.receipt())
			checked, checkErr := CheckMac(p)
			_, changed, err := InstallMac(macBundle(t, "0.1.0-dev.18"), "arm64", p)
			if tc.refuse {
				if err == nil || checkErr == nil {
					t.Fatalf("overwrote an app that is not ours: %v %v", err, checkErr)
				}
				if after, _ := os.ReadFile(p.receipt()); string(after) != string(before) {
					t.Fatal("receipt changed after refusing")
				}
				if _, e := os.Lstat(p.app()); e != nil {
					t.Fatal("refused install still moved the app")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if changed == nil || !strings.Contains(changed.Error(), tc.want) {
				t.Fatalf("changed = %v, want %q", changed, tc.want)
			}
			if checkErr != nil || checked == nil || checked.Error() != changed.Error() {
				t.Fatalf("CheckMac = %v %v, install reported %v", checked, checkErr, changed)
			}
			if v := appVersion(t, p); v != "0.1.0-dev.18 "+MacApp+"/Contents/MacOS/0c-server" {
				t.Fatalf("installed %q", v)
			}
			if c, err := CheckMac(p); c != nil || err != nil {
				t.Fatalf("new app not clean: %v %v", c, err)
			}
			if b, _ := os.ReadFile(outside); string(b) != "not ours" {
				t.Fatal("followed a link out of the app")
			}
			// 舊 App 原樣保留在備份，可回復；回復時提示放回的 App 與收據不符。
			back, err := RollbackMac(p)
			if tc.name == "whole app missing" {
				// 收據啟用中、App 卻不見：備份裡沒有可放回的 App，拒絕回復，新版保持安裝。
				if err == nil || appVersion(t, p) == "absent" {
					t.Fatalf("rollback to a missing app: %+v %v", back, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if back.Restored == nil || !strings.Contains(back.Restored.Error(), tc.want) {
				t.Fatalf("rollback restored %v, want %q", back.Restored, tc.want)
			}
			if c, err := CheckMac(p); err != nil || c == nil || c.Error() != changed.Error() {
				t.Fatalf("restored app check %v %v", c, err)
			}
		})
	}
}

func TestMacChangesListIsSortedAndBounded(t *testing.T) {
	app := filepath.Join(t.TempDir(), MacApp)
	os.MkdirAll(filepath.Join(app, "Contents"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents/Info.plist"), plistWithID("com.biggo.inputmethod.0c"), 0o644)
	files := map[string]string{}
	files["Contents/Info.plist"], _ = Hash(filepath.Join(app, "Contents/Info.plist"))
	for i := 0; i < 12; i++ {
		files["Contents/MacOS/f"+string(rune('a'+i))] = strings.Repeat("0", 64)
	}
	changed, err := checkMacApp(app, files)
	if err != nil {
		t.Fatal(err)
	}
	c := changed.(*MacAppChanges)
	if len(c.Files) != 12 || c.Files[0] != "缺少 Contents/MacOS/fa" || c.Files[11] != "缺少 Contents/MacOS/fl" {
		t.Fatalf("files %v", c.Files)
	}
	if msg := c.Error(); !strings.Contains(msg, "等 12 項") || strings.Contains(msg, "fk") {
		t.Fatalf("message %q", msg)
	}
}

func TestMacRefusesInvalidReceiptApp(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "0c")}
	os.MkdirAll(p.Support, 0o700)
	writeJSON(p.receipt(), &MacReceipt{Schema: 1, Product: "0c", Version: "0.1.0-dev.17", App: "../../Applications/Other.app", Active: true})
	if _, _, err := InstallMac(macBundle(t, "0.1.0-dev.18"), "arm64", p); err == nil {
		t.Fatal("accepted a receipt app outside Input Methods")
	}
}

// 沒有收據時照舊：原有的 App 整包移入備份（0c 以前的原型），不提示不符。
func TestMacInstallWithoutReceipt(t *testing.T) {
	home := t.TempDir()
	p := MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "0c")}
	os.MkdirAll(filepath.Join(p.app(), "Contents/MacOS"), 0o755)
	os.WriteFile(filepath.Join(p.app(), "Contents/MacOS/0c-server"), []byte("hand copied"), 0o755)
	_, changed, err := InstallMac(macBundle(t, "0.1.0-dev.18"), "arm64", p)
	if err != nil || changed != nil {
		t.Fatalf("install without receipt: %v %v", changed, err)
	}
	if _, err := RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.app(), "Contents/MacOS/0c-server")); string(b) != "hand copied" {
		t.Fatal("app without receipt not kept in backup")
	}
}

// 回復與移除：目前的 App 被改動時照常進行並提示；備份中的 App 被改動時照常放回並提示；
// 備份中的 App 不是靈犀注音的、不見或是連結時拒絕回復，不動目前的安裝。
func TestMacRollbackAndUninstallWithModifiedApps(t *testing.T) {
	setup := func(t *testing.T) (MacPaths, string) {
		home := t.TempDir()
		p := MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "0c")}
		for _, v := range []string{"0.1.0-dev.17", "0.1.0-dev.18"} {
			if _, _, err := InstallMac(macBundle(t, v), "arm64", p); err != nil {
				t.Fatal(err)
			}
		}
		saved, err := MacRollbackTarget(p)
		if err != nil {
			t.Fatal(err)
		}
		return p, saved
	}
	t.Run("current changed", func(t *testing.T) {
		p, _ := setup(t)
		os.WriteFile(filepath.Join(p.app(), "Contents/MacOS/0c"), []byte("patched"), 0o755)
		if _, err := MacRollbackTarget(p); err != nil {
			t.Fatal(err)
		}
		r, err := RollbackMac(p)
		if err != nil {
			t.Fatal(err)
		}
		if r.Changed == nil || !strings.Contains(r.Changed.Error(), "已改動 Contents/MacOS/0c") || r.Restored != nil {
			t.Fatalf("rollback %+v", r)
		}
		if appVersion(t, p) != "0.1.0-dev.17 "+MacApp+"/Contents/MacOS/0c-server" {
			t.Fatalf("rolled back to %q", appVersion(t, p))
		}
		// 撤回回復：放回的是剛才被改動的 dev.18，照常放回並提示。
		again, err := RollbackMac(p)
		if err != nil || again.Restored == nil || !strings.Contains(again.Restored.Error(), "已改動 Contents/MacOS/0c") {
			t.Fatalf("undo rollback %+v %v", again, err)
		}
	})
	t.Run("backup changed", func(t *testing.T) {
		p, saved := setup(t)
		os.WriteFile(filepath.Join(saved, "Contents/MacOS/0c-server"), []byte("patched"), 0o755)
		os.Remove(filepath.Join(saved, "Contents/Resources/data/report.txt"))
		r, err := RollbackMac(p)
		if err != nil {
			t.Fatal(err)
		}
		if r.Restored == nil || !strings.Contains(r.Restored.Error(), "已改動 Contents/MacOS/0c-server") ||
			!strings.Contains(r.Restored.Error(), "缺少 Contents/Resources/data/report.txt") {
			t.Fatalf("restored %v", r.Restored)
		}
		if b, _ := os.ReadFile(filepath.Join(p.app(), "Contents/MacOS/0c-server")); string(b) != "patched" {
			t.Fatal("backup app not restored as it was")
		}
	})
	for name, modify := range map[string]func(string){
		"backup is not ours": func(app string) {
			os.WriteFile(filepath.Join(app, "Contents/Info.plist"), plistWithID("com.example.other"), 0o644)
		},
		"backup app missing": func(app string) { os.RemoveAll(app) },
		"backup app is a link": func(app string) {
			moved := app + ".real"
			os.Rename(app, moved)
			os.Symlink(moved, app)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, saved := setup(t)
			modify(saved)
			before, _ := os.ReadFile(p.receipt())
			if _, err := RollbackMac(p); err == nil {
				t.Fatal("rolled back to an app that is not ours")
			}
			if after, _ := os.ReadFile(p.receipt()); string(after) != string(before) || appVersion(t, p) != "0.1.0-dev.18 "+MacApp+"/Contents/MacOS/0c-server" {
				t.Fatal("refused rollback changed the installation")
			}
		})
	}
	t.Run("uninstall changed", func(t *testing.T) {
		p, _ := setup(t)
		os.Remove(filepath.Join(p.app(), "Contents/MacOS/0c-server"))
		changed, err := UninstallMac(p)
		if err != nil || changed == nil || !strings.Contains(changed.Error(), "缺少 Contents/MacOS/0c-server") {
			t.Fatalf("uninstall %v %v", changed, err)
		}
		if _, err := os.Lstat(p.app()); !os.IsNotExist(err) {
			t.Fatal("uninstall left the app")
		}
		if c, err := CheckMac(p); c != nil || err != nil {
			t.Fatalf("inactive receipt checked: %v %v", c, err)
		}
		r, err := RollbackMac(p)
		if err != nil || r.Restored == nil || r.App != p.app() {
			t.Fatalf("undo uninstall %+v %v", r, err)
		}
	})
	t.Run("uninstall not ours", func(t *testing.T) {
		p, _ := setup(t)
		os.WriteFile(filepath.Join(p.app(), "Contents/Info.plist"), plistWithID("com.example.other"), 0o644)
		if _, err := UninstallMac(p); err == nil {
			t.Fatal("moved an app that is not ours")
		}
		if _, err := os.Lstat(p.app()); err != nil {
			t.Fatal("refused uninstall moved the app")
		}
	})
}
