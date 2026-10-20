package installs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func touch(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func layout(t *testing.T) Layout {
	root := t.TempDir()
	return Layout{Home: filepath.Join(root, "home"), Libexec: filepath.Join(root, "usr/libexec/0c"),
		Data: filepath.Join(root, "usr/share/0c"), Bin: filepath.Join(root, "usr/bin")}
}

func personal(t *testing.T, l Layout) {
	touch(t, filepath.Join(l.Home, ".local/bin/0c-server"), 0o755)
	touch(t, filepath.Join(l.Home, ".local/bin/0c-update"), 0o755)
	touch(t, filepath.Join(l.Home, ".local/share/zuin/bin/0c-update"), 0o755)
}

func system(t *testing.T, l Layout, distribution string) {
	touch(t, filepath.Join(l.Libexec, "0c-server"), 0o755)
	touch(t, filepath.Join(l.Libexec, "0c-update"), 0o755)
	touch(t, filepath.Join(l.Bin, "0c-settings"), 0o755)
	if distribution != "" {
		os.MkdirAll(l.Data, 0o755)
		if err := os.WriteFile(filepath.Join(l.Data, DistributionFile), []byte(distribution), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 官網安裝的行為不變：引擎不帶參數（自己依 engine.json 轉交），0c-update 先找穩定副本再找 ~/.local/bin。
func TestPersonalInstallIsUnchanged(t *testing.T) {
	l := layout(t)
	personal(t, l)
	server, args := l.Server()
	if server != filepath.Join(l.Home, ".local/bin/0c-server") || args != nil {
		t.Fatalf("server = %q %q", server, args)
	}
	if got := l.Updater(); got != filepath.Join(l.Home, ".local/share/zuin/bin/0c-update") {
		t.Fatalf("updater = %q", got)
	}
	os.Remove(filepath.Join(l.Home, ".local/share/zuin/bin/0c-update"))
	if got := l.Updater(); got != filepath.Join(l.Home, ".local/bin/0c-update") {
		t.Fatalf("updater without stable copy = %q", got)
	}
}

func TestNothingInstalledFallsBackLikeBefore(t *testing.T) {
	l := layout(t)
	if server, args := l.Server(); server != "0c-server" || args != nil {
		t.Fatalf("server = %q %q", server, args)
	}
	if got := l.Updater(); got != filepath.Join(l.Home, ".local/share/zuin/bin/0c-update") {
		t.Fatalf("updater = %q", got)
	}
	if all := l.All(); len(all) != 0 {
		t.Fatalf("installs = %+v", all)
	}
}

func TestSystemInstallPassesItsDictionary(t *testing.T) {
	l := layout(t)
	system(t, l, `{"schema":1,"updates":"package-manager","version":"0.2.3"}`)
	server, args := l.Server()
	if server != filepath.Join(l.Libexec, "0c-server") || !reflect.DeepEqual(args, []string{"-data", filepath.Join(l.Data, "data")}) {
		t.Fatalf("server = %q %q", server, args)
	}
	if got := l.Updater(); got != filepath.Join(l.Libexec, "0c-update") {
		t.Fatalf("updater = %q", got)
	}
	all := l.All()
	if len(all) != 1 || all[0].Kind != KindSystem || !all[0].Active || all[0].Updates != UpdatesPackageManager || all[0].Version != "0.2.3" ||
		all[0].Settings != filepath.Join(l.Bin, "0c-settings") {
		t.Fatalf("installs = %+v", all)
	}
}

// 兩份並存時官網安裝優先（使用者自己裝的比較新）；系統那份照樣列出，設定程式「問題排解」顯示。
func TestPersonalWinsOverSystem(t *testing.T) {
	l := layout(t)
	personal(t, l)
	system(t, l, `{"schema":1,"updates":"package-manager"}`)
	if server, args := l.Server(); server != filepath.Join(l.Home, ".local/bin/0c-server") || args != nil {
		t.Fatalf("server = %q %q", server, args)
	}
	if got := l.Updater(); got != filepath.Join(l.Home, ".local/share/zuin/bin/0c-update") {
		t.Fatalf("updater = %q", got)
	}
	all := l.All()
	if len(all) != 2 || all[0].Kind != KindPersonal || !all[0].Active || all[1].Kind != KindSystem || all[1].Active {
		t.Fatalf("installs = %+v", all)
	}
}

// 不能執行的檔案不算（例如解除安裝到一半）。
func TestNonExecutableIsIgnored(t *testing.T) {
	l := layout(t)
	touch(t, filepath.Join(l.Home, ".local/bin/0c-server"), 0o644)
	system(t, l, "")
	if server, _ := l.Server(); server != filepath.Join(l.Libexec, "0c-server") {
		t.Fatalf("server = %q", server)
	}
}

func TestPackageManagedNeedsSystemExecutableAndMarker(t *testing.T) {
	l := layout(t)
	personal(t, l)
	system(t, l, `{"schema":1,"updates":"package-manager"}`)
	if !l.PackageManaged(filepath.Join(l.Libexec, "0c-update")) {
		t.Fatal("system 0c-update should be package-managed")
	}
	// 官網安裝的 0c-update 不受系統套件影響。
	for _, exe := range []string{filepath.Join(l.Home, ".local/bin/0c-update"), filepath.Join(l.Home, ".local/share/zuin/bin/0c-update")} {
		if l.PackageManaged(exe) {
			t.Fatalf("%s should update itself", exe)
		}
	}
	// 系統目錄裡的連結也認得。
	link := filepath.Join(t.TempDir(), "0c-update")
	if err := os.Symlink(filepath.Join(l.Libexec, "0c-update"), link); err != nil {
		t.Fatal(err)
	}
	if !l.PackageManaged(link) {
		t.Fatal("symlink to the system 0c-update should be package-managed")
	}
	for _, marker := range []string{`{"schema":1,"updates":"self"}`, `{"schema":2,"updates":"package-manager"}`, `not json`} {
		os.WriteFile(filepath.Join(l.Data, DistributionFile), []byte(marker), 0o644)
		if l.PackageManaged(filepath.Join(l.Libexec, "0c-update")) {
			t.Fatalf("marker %s should not be package-managed", marker)
		}
	}
	os.Remove(filepath.Join(l.Data, DistributionFile))
	if l.PackageManaged(filepath.Join(l.Libexec, "0c-update")) {
		t.Fatal("no marker should not be package-managed")
	}
}
