//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// macBundle 造一個含巢狀設定程式的 macOS 套件。withSettings 為假時是沒有設定程式的舊式套件。
func macBundle(t *testing.T, version string, withSettings bool) string {
	t.Helper()
	root := t.TempDir()
	release := update.Release{Schema: 1, Product: "0c", Version: version, Channel: "internal-preview", OS: "darwin", Arch: "arm64", Files: map[string]string{}}
	names := []string{"0c-update", update.MacApp + "/Contents/Info.plist", update.MacApp + "/Contents/MacOS/0c", update.MacApp + "/Contents/MacOS/0c-server"}
	for _, n := range []string{"manifest.json", "lexicon.bin", "lm.bin", "NOTICE.txt", "report.txt"} {
		names = append(names, update.MacApp+"/Contents/Resources/data/"+n)
	}
	if withSettings {
		for _, n := range []string{"Contents/Info.plist", "Contents/MacOS/0c-settings", "Contents/Resources/AppIcon.icns"} {
			names = append(names, update.MacApp+"/"+settingsAppPrefix+"/"+n)
		}
	}
	for _, name := range names {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(version+" "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		release.Files[name], _ = update.Hash(p)
	}
	b, _ := json.Marshal(release)
	if err := os.WriteFile(filepath.Join(root, "release.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// 實際安裝一個套件，再用 open-settings 的查找流程驗證安裝後的巢狀設定程式：安裝器複製出來的檔案與權限
// 要通過 LocateBundle 的檢查；升級到沒有設定程式的版本後不再找到它；回復後又找得到。
func TestInstalledMacAppExposesTheNestedSettingsApp(t *testing.T) {
	home := t.TempDir()
	p := update.MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	receipt := filepath.Join(p.Support, "install-receipt.json")
	locate := func() (string, error) {
		app, files := settingsAppFromReceipt(receipt, p.InputMethods)
		if app == "" {
			return "", settingsapp.ErrNotFound
		}
		location, err := settingsapp.LocateBundle(app, files)
		return location.Path, err
	}

	if _, _, err := update.InstallMac(macBundle(t, "0.1.0-dev.42", false), "arm64", p); err != nil {
		t.Fatal(err)
	}
	if _, err := locate(); !errors.Is(err, settingsapp.ErrNotFound) {
		t.Fatalf("舊版套件沒有設定程式，應回 ErrNotFound：%v", err)
	}

	if _, _, err := update.InstallMac(macBundle(t, "0.1.0-dev.43", true), "arm64", p); err != nil {
		t.Fatal(err)
	}
	app, err := locate()
	if err != nil {
		t.Fatalf("安裝後的設定程式應通過檢查：%v", err)
	}
	if want := filepath.Join(p.InputMethods, update.MacApp, settingsAppPrefix); app != want {
		t.Fatalf("%s != %s", app, want)
	}

	executable := filepath.Join(app, "Contents/MacOS/0c-settings")
	if err := os.WriteFile(executable, []byte("patched"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := locate(); err == nil || errors.Is(err, settingsapp.ErrNotFound) {
		t.Fatalf("被改過的設定程式要回驗證錯誤，不是找不到：%v", err)
	}

	// 回復只在安裝的內容與收據相符時才進行，所以先還原被改的檔案。
	original := "0.1.0-dev.43 " + update.MacApp + "/" + settingsAppPrefix + "/Contents/MacOS/0c-settings"
	if err := os.WriteFile(executable, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := locate(); err != nil {
		t.Fatalf("還原後應再通過檢查：%v", err)
	}
	if _, err := update.RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	if _, err := locate(); !errors.Is(err, settingsapp.ErrNotFound) {
		t.Fatalf("回復到舊版後沒有設定程式：%v", err)
	}
}
