//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settingsapp"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func macLauncherBundle(t *testing.T, version string) string {
	t.Helper()
	bundle := macBundle(t, version, true)
	info, err := os.ReadFile("../../platforms/macos/SettingsLauncher/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{
		"Contents/MacOS/0c-update":                                                      []byte(version + " updater"),
		settingsLauncherPrefix + "/Contents/Info.plist":                                 info,
		settingsLauncherPrefix + "/Contents/MacOS/" + settingsapp.MacLauncherExecutable: []byte(version + " launcher"),
		settingsLauncherPrefix + "/Contents/_CodeSignature/CodeResources":               []byte(version + " signature"),
		settingsLauncherPrefix + "/Contents/CodeResources":                              []byte(version + " ticket"),
	}
	var release update.Release
	b, _ := os.ReadFile(filepath.Join(bundle, "release.json"))
	json.Unmarshal(b, &release)
	for name, b := range contents {
		path := filepath.Join(bundle, update.MacApp, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, b, 0o755); err != nil {
			t.Fatal(err)
		}
		release.Files[update.MacApp+"/"+name], _ = update.Hash(path)
	}
	b, _ = json.Marshal(release)
	if err := os.WriteFile(filepath.Join(bundle, "release.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestMacSettingsEntryTracksInstallUpdateRollbackAndRemoval(t *testing.T) {
	home := t.TempDir()
	p := update.MacPaths{InputMethods: filepath.Join(home, "Library/Input Methods"), Support: filepath.Join(home, "Library/Application Support/0c")}
	receipt, apps := filepath.Join(p.Support, "install-receipt.json"), filepath.Join(home, "Applications")
	stable := filepath.Join(p.Support, "bin/0c-update")
	sync := func() {
		t.Helper()
		if _, err := syncMacSettingsEntry(apps, receipt, p.InputMethods, stable); err != nil {
			t.Fatal(err)
		}
	}
	check := func(version string) {
		t.Helper()
		sync()
		for path, want := range map[string]string{stable: version + " updater", filepath.Join(apps, settingsapp.MacLauncherName, "Contents/MacOS", settingsapp.MacLauncherExecutable): version + " launcher"} {
			b, err := os.ReadFile(path)
			if err != nil || string(b) != want {
				t.Fatalf("%s: %q %v", path, b, err)
			}
		}
	}
	for _, version := range []string{"0.1.0-dev.55.1", "0.1.0-dev.55.2"} {
		if _, _, err := update.InstallMac(macLauncherBundle(t, version), "arm64", p); err != nil {
			t.Fatal(err)
		}
		check(version)
	}
	if _, err := update.RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	check("0.1.0-dev.55.1")
	if _, err := update.UninstallMac(p); err != nil {
		t.Fatal(err)
	}
	sync()
	if _, err := os.Stat(filepath.Join(apps, settingsapp.MacLauncherName)); !os.IsNotExist(err) {
		t.Fatal("移除後仍有入口")
	}
	if _, err := update.RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	check("0.1.0-dev.55.1")
	if _, _, err := update.InstallMac(macBundle(t, "0.1.0-dev.54", false), "arm64", p); err != nil {
		t.Fatal(err)
	}
	sync()
	if _, err := os.Stat(filepath.Join(apps, settingsapp.MacLauncherName)); !os.IsNotExist(err) {
		t.Fatal("沒有設定程式的版本應移除入口")
	}
	if _, err := update.RollbackMac(p); err != nil {
		t.Fatal(err)
	}
	check("0.1.0-dev.55.1")
}

func TestMacSettingsEntryPreservesEntryOnVerificationFailureAndRepairsDeletedEntry(t *testing.T) {
	home := t.TempDir()
	p := update.MacPaths{InputMethods: filepath.Join(home, "Input Methods"), Support: filepath.Join(home, "Support")}
	if _, _, err := update.InstallMac(macLauncherBundle(t, "0.1.0-dev.55.1"), "arm64", p); err != nil {
		t.Fatal(err)
	}
	receipt, apps, stable := filepath.Join(p.Support, "install-receipt.json"), filepath.Join(home, "Applications"), filepath.Join(p.Support, "bin/0c-update")
	sync := func() error { _, err := syncMacSettingsEntry(apps, receipt, p.InputMethods, stable); return err }
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(apps, settingsapp.MacLauncherName)
	os.RemoveAll(dest)
	if err := sync(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(dest)
	program := filepath.Join(p.InputMethods, update.MacApp, settingsAppPrefix, "Contents/MacOS/0c-settings")
	os.WriteFile(program, []byte("patched"), 0o755)
	if err := sync(); err == nil {
		t.Fatal("變造的設定程式不能通過")
	}
	after, _ := os.Stat(dest)
	if !os.SameFile(before, after) {
		t.Fatal("核對失敗卻移除或改寫入口")
	}
	os.WriteFile(receipt, []byte(`{"app":"../../escape","active":true,"files":{}}`), 0o600)
	if err := sync(); err == nil {
		t.Fatal("不可從任意 App 取程式")
	}
}
