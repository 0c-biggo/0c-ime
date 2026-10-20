//go:build !windows

package settingsapp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/update"
)

func fakeLauncher(t *testing.T) (string, map[string]string) {
	t.Helper()
	app := filepath.Join(t.TempDir(), "launcher.app")
	files := map[string]string{}
	for name, content := range map[string]string{
		"Contents/Info.plist":                     `<plist><dict><key>CFBundleIdentifier</key><string>` + MacLauncherID + `</string><key>0cManagedSettingsLauncher</key><true/></dict></plist>`,
		"Contents/MacOS/" + MacLauncherExecutable: "launcher v1",
		"Contents/Resources/AppIcon.icns":         "icon",
		"Contents/_CodeSignature/CodeResources":   "signed resources",
		"Contents/CodeResources":                  "notarization ticket",
	} {
		path := filepath.Join(app, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		files[name], _ = update.Hash(path)
	}
	return app, files
}

func TestMacLauncherCopiesSignatureAndTicketAndOnlyReplacesChangedContent(t *testing.T) {
	source, files := fakeLauncher(t)
	apps := filepath.Join(t.TempDir(), "Applications")
	dest := filepath.Join(apps, MacLauncherName)
	if changed, err := EnsureMacLauncher(apps, source, files); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	before, _ := os.Stat(dest)
	if err := VerifyBundle(dest, files); err != nil {
		t.Fatal(err)
	}
	if changed, err := EnsureMacLauncher(apps, source, files); err != nil || changed {
		t.Fatalf("不應重寫：%v %v", changed, err)
	}
	after, _ := os.Stat(dest)
	if !os.SameFile(before, after) {
		t.Fatal("相同內容卻換了 App")
	}
	program := "Contents/MacOS/" + MacLauncherExecutable
	os.WriteFile(filepath.Join(source, program), []byte("launcher v2"), 0o755)
	files[program], _ = update.Hash(filepath.Join(source, program))
	if changed, err := EnsureMacLauncher(apps, source, files); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if err := VerifyBundle(dest, files); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMacLauncher(apps); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatal("入口還在")
	}
}

func TestMacLauncherKeepsUnmanagedAppsAndRejectsLinksAndTamperedSource(t *testing.T) {
	for _, kind := range []string{"unmanaged", "destination link", "Applications link", "source changed", "source extra"} {
		t.Run(kind, func(t *testing.T) {
			source, files := fakeLauncher(t)
			apps := filepath.Join(t.TempDir(), "Applications")
			os.Mkdir(apps, 0o755)
			dest := filepath.Join(apps, MacLauncherName)
			switch kind {
			case "unmanaged":
				os.MkdirAll(filepath.Join(dest, "Contents"), 0o755)
				os.WriteFile(filepath.Join(dest, "Contents/Info.plist"), []byte(`<plist><dict><key>CFBundleIdentifier</key><string>another.app</string></dict></plist>`), 0o644)
			case "destination link":
				os.Symlink(source, dest)
			case "Applications link":
				os.Remove(apps)
				os.Symlink(filepath.Dir(source), apps)
			case "source changed":
				os.WriteFile(filepath.Join(source, "Contents/Info.plist"), []byte("tampered"), 0o644)
			case "source extra":
				os.WriteFile(filepath.Join(source, "extra"), []byte("tampered"), 0o644)
			}
			if _, err := EnsureMacLauncher(apps, source, files); err == nil {
				t.Fatal("應拒絕")
			}
			if kind == "unmanaged" {
				if err := RemoveMacLauncher(apps); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(dest); err != nil {
					t.Fatal("刪了別人的 App")
				}
			}
			if kind == "destination link" {
				if err := RemoveMacLauncher(apps); err == nil {
					t.Fatal("移除也須拒絕連結")
				}
				if _, err := os.Lstat(dest); err != nil {
					t.Fatal("動了連結")
				}
			}
		})
	}
}

func TestMacLauncherConcurrentRepairLeavesOneCompleteBundle(t *testing.T) {
	source, files := fakeLauncher(t)
	apps := filepath.Join(t.TempDir(), "Applications")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := EnsureMacLauncher(apps, source, files); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := VerifyBundle(filepath.Join(apps, MacLauncherName), files); err != nil {
		t.Fatal(err)
	}
}

func TestMacUpdaterAtomicRepairAndRejection(t *testing.T) {
	source := filepath.Join(t.TempDir(), "updater")
	if err := os.WriteFile(source, []byte("current updater"), 0o755); err != nil {
		t.Fatal(err)
	}
	want, _ := update.Hash(source)
	stable := filepath.Join(t.TempDir(), "bin/0c-update")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := EnsureMacUpdater(source, want, stable); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := Verify(stable, want); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(stable)
	if err := EnsureMacUpdater(source, want, stable); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(stable)
	if !os.SameFile(before, after) {
		t.Fatal("相同穩定更新器不重寫")
	}
	os.Remove(stable)
	os.Symlink(source, stable)
	if err := EnsureMacUpdater(source, want, stable); err == nil {
		t.Fatal("不可跟隨穩定更新器的連結")
	}
	os.Remove(stable)
	os.WriteFile(source, []byte("tampered"), 0o755)
	if err := EnsureMacUpdater(source, want, stable); err == nil {
		t.Fatal("來源需依收據核對")
	}
}
