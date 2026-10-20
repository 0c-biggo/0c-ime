//go:build !windows && !darwin

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// 以真正的 install_package.py 完整安裝 dev.18，模擬核心更新到 dev.19 後對齊 ~/.local/bin/0c-update，
// 再以安裝器回復：核心指標與換過的 0c-update、收據都必須照常核對，回到安裝前的狀態。
func TestPathUpdaterFollowsCoreUpdateAndKeepsRollback(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("需要 python3")
	}
	_, self, _, _ := runtime.Caller(0)
	scripts := filepath.Join(filepath.Dir(self), "..", "..", "scripts")
	root := t.TempDir()
	home, bundle := filepath.Join(root, "home"), filepath.Join(root, "bundle")
	base := filepath.Join(home, ".local/share/zuin")
	python := func(code string) {
		t.Helper()
		cmd := exec.Command("python3", "-c", "import sys, json, pathlib; sys.path.insert(0, sys.argv[1]); import install_package as i; H=pathlib.Path(sys.argv[2]); B=pathlib.Path(sys.argv[3]); "+code, scripts, home, bundle)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", code, err, out)
		}
	}
	os.MkdirAll(home, 0o700)
	python(`
for n in i.PAYLOAD:
    p = B / n; p.parent.mkdir(parents=True, exist_ok=True); p.write_text("dev.18 " + n + ("\nLibrary=lib0c\n" if n == i.ADDON_CONF else ""))
i.install(B, H, {"version": "0.1.0-dev.18"})`)
	target := filepath.Join(home, pathUpdaterRel)
	receipt := func() map[string]any {
		var r map[string]any
		b, _ := os.ReadFile(filepath.Join(home, receiptRel))
		json.Unmarshal(b, &r)
		return r
	}
	backup := receipt()["backup"].(string)

	// 核心更新：版本目錄帶自己的 0c-update，排程穩定副本換成同一份，指標指向它。
	core := filepath.Join(base, "versions/0.1.0-dev.19-c-aaaaaaaaaaaa")
	os.MkdirAll(filepath.Join(core, "data"), 0o700)
	os.WriteFile(filepath.Join(core, enginedir.Executable()), []byte("engine"), 0o700)
	os.WriteFile(filepath.Join(core, "data/manifest.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(core, "0c-update"), []byte("dev.19 updater"), 0o700)
	os.MkdirAll(filepath.Join(base, "bin"), 0o700)
	os.WriteFile(stableUpdater(base), []byte("dev.19 updater"), 0o700)
	if err := enginedir.Write(base, enginedir.Current{Directory: "versions/0.1.0-dev.19-c-aaaaaaaaaaaa", Version: "0.1.0-dev.19"}); err != nil {
		t.Fatal(err)
	}

	if err := syncPathUpdater(base, home); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "dev.19 updater" {
		t.Fatalf("PATH 上的 0c-update 未更新：%q", b)
	}
	want, _ := update.Hash(target)
	if receipt()["files"].(map[string]any)[pathUpdaterRel] != want {
		t.Fatal("收據沒有記錄新的 0c-update")
	}
	if err := syncPathUpdater(base, home); err != nil { // 再跑一次不應改變任何東西
		t.Fatal(err)
	}

	// 安裝器的回復照常核對，並取回完整安裝前的狀態（首次安裝：刪除）。
	python(`i.rollback(H, pathlib.Path("` + backup + `"))`)
	if _, err := os.Stat(filepath.Join(base, "engine.json")); !os.IsNotExist(err) {
		t.Fatal("回復完整安裝後應移除核心更新寫入的引擎指標")
	}
	// 首次安裝的回復：回到沒有 0c 安裝，排程、穩定副本、更新狀態與核心目錄一併清掉。
	os.MkdirAll(filepath.Join(base, "update"), 0o700)
	os.WriteFile(filepath.Join(base, "update/state.json"), []byte("{}"), 0o600)
	unscheduled := false
	afterRollback(base, func() error { unscheduled = true; return nil })
	for _, rel := range []string{"bin", "update", "versions/0.1.0-dev.19-c-aaaaaaaaaaaa"} {
		if _, err := os.Stat(filepath.Join(base, rel)); !os.IsNotExist(err) {
			t.Fatalf("回到未安裝後仍留著 %s", rel)
		}
	}
	if !unscheduled {
		t.Fatal("回到未安裝後應移除自動更新排程")
	}
	if _, err := os.Stat(filepath.Join(base, "user.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("回復後 PATH 上的 0c-update 應移除（首次安裝）")
	}
}

// 使用者改過的檔案、沒有更新的核心、指標版本不比完整安裝新時都不動。
func TestPathUpdaterLeavesUserChangesAlone(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("需要 python3")
	}
	_, self, _, _ := runtime.Caller(0)
	scripts := filepath.Join(filepath.Dir(self), "..", "..", "scripts")
	root := t.TempDir()
	home, bundle := filepath.Join(root, "home"), filepath.Join(root, "bundle")
	base := filepath.Join(home, ".local/share/zuin")
	os.MkdirAll(home, 0o700)
	cmd := exec.Command("python3", "-c", `import sys, pathlib; sys.path.insert(0, sys.argv[1]); import install_package as i
H=pathlib.Path(sys.argv[2]); B=pathlib.Path(sys.argv[3])
for n in i.PAYLOAD:
    p = B / n; p.parent.mkdir(parents=True, exist_ok=True); p.write_text("dev.18 " + n + ("\nLibrary=lib0c\n" if n == i.ADDON_CONF else ""))
i.install(B, H, {"version": "0.1.0-dev.18"})`, scripts, home, bundle)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	target := filepath.Join(home, pathUpdaterRel)
	os.MkdirAll(filepath.Join(base, "bin"), 0o700)
	os.WriteFile(stableUpdater(base), []byte("dev.19 updater"), 0o700)
	// 沒有核心指標：不動。
	syncPathUpdater(base, home)
	if b, _ := os.ReadFile(target); !strings.HasPrefix(string(b), "dev.18") {
		t.Fatal("沒有核心更新卻換了檔案")
	}
	core := filepath.Join(base, "versions/0.1.0-dev.19-c-aaaaaaaaaaaa")
	os.MkdirAll(filepath.Join(core, "data"), 0o700)
	os.WriteFile(filepath.Join(core, enginedir.Executable()), []byte("engine"), 0o700)
	os.WriteFile(filepath.Join(core, "data/manifest.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(core, "0c-update"), []byte("dev.19 updater"), 0o700)
	enginedir.Write(base, enginedir.Current{Directory: "versions/0.1.0-dev.19-c-aaaaaaaaaaaa", Version: "0.1.0-dev.19"})
	// 穩定副本與核心版本目錄內的不同：不動。
	os.WriteFile(stableUpdater(base), []byte("something else"), 0o700)
	syncPathUpdater(base, home)
	if b, _ := os.ReadFile(target); !strings.HasPrefix(string(b), "dev.18") {
		t.Fatal("穩定副本來源不明卻換了檔案")
	}
	// 使用者改過：不動。
	os.WriteFile(stableUpdater(base), []byte("dev.19 updater"), 0o700)
	os.WriteFile(target, []byte("user edit"), 0o755)
	syncPathUpdater(base, home)
	if b, _ := os.ReadFile(target); string(b) != "user edit" {
		t.Fatal("覆蓋了使用者改過的檔案")
	}
}

// 舊版 install.py 見到完整安裝後重新出現的 engine.json 就拒絕回復；0c-update 先移開，失敗時放回。
func TestRollbackSetsCorePointerAside(t *testing.T) {
	base, backup := t.TempDir(), t.TempDir()
	pointer := filepath.Join(base, "engine.json")
	os.WriteFile(filepath.Join(backup, "transaction.json"), []byte(`{".local/share/zuin/engine.json": {"before": null, "after": null}}`), 0o600)
	oldInstaller := func(fail bool) func() error {
		return func() error {
			if _, err := os.Stat(pointer); err == nil {
				return os.ErrExist // 「安裝後已移除的舊檔又出現」
			}
			if fail {
				return os.ErrPermission
			}
			return nil
		}
	}
	os.WriteFile(pointer, []byte(`{"version": "0.1.0-dev.19"}`), 0o600)
	if err := withPointerSetAside(base, backup, oldInstaller(true)); err == nil {
		t.Fatal("回復失敗應回報錯誤")
	}
	if b, _ := os.ReadFile(pointer); !strings.Contains(string(b), "dev.19") {
		t.Fatal("回復失敗後應放回原本的指標")
	}
	if err := withPointerSetAside(base, backup, oldInstaller(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pointer); !os.IsNotExist(err) {
		t.Fatal("安裝前沒有指標：回復後應移除")
	}
	if _, err := os.Stat(pointer + ".rollback"); !os.IsNotExist(err) {
		t.Fatal("暫存的指標沒有清掉")
	}
}

// 回復到上一次完整安裝：仍有安裝，排程與穩定副本保留；沒被指標引用的核心目錄清掉。
func TestRollbackToPreviousInstallKeepsSchedule(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("需要 python3")
	}
	_, self, _, _ := runtime.Caller(0)
	scripts := filepath.Join(filepath.Dir(self), "..", "..", "scripts")
	root := t.TempDir()
	home, bundle := filepath.Join(root, "home"), filepath.Join(root, "bundle")
	base := filepath.Join(home, ".local/share/zuin")
	os.MkdirAll(home, 0o700)
	cmd := exec.Command("python3", "-c", `import sys, pathlib; sys.path.insert(0, sys.argv[1]); import install_package as i
H=pathlib.Path(sys.argv[2]); B=pathlib.Path(sys.argv[3])
for v in ("0.1.0-dev.17", "0.1.0-dev.18"):
    for n in i.PAYLOAD:
        p = B / n; p.parent.mkdir(parents=True, exist_ok=True); p.write_text(v + " " + n + ("\nLibrary=lib0c\n" if n == i.ADDON_CONF else ""))
    last = i.install(B, H, {"version": v})
i.rollback(H, last)`, scripts, home, bundle)
	os.MkdirAll(filepath.Join(base, "bin"), 0o700)
	os.WriteFile(stableUpdater(base), []byte("updater"), 0o700)
	os.MkdirAll(filepath.Join(base, "versions/0.1.0-dev.19-c-aaaaaaaaaaaa"), 0o700)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	unscheduled := false
	afterRollback(base, func() error { unscheduled = true; return nil })
	if unscheduled {
		t.Fatal("仍有安裝時不應移除排程")
	}
	if _, err := os.Stat(stableUpdater(base)); err != nil {
		t.Fatal("仍有安裝時應保留排程用的穩定副本")
	}
	if _, err := os.Stat(filepath.Join(base, "versions/0.1.0-dev.19-c-aaaaaaaaaaaa")); !os.IsNotExist(err) {
		t.Fatal("沒被引擎指標引用的核心目錄應清掉")
	}
}
