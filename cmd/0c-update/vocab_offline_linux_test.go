package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/installs"
)

func writeExe(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 0c-update 找字典（以及字典旁的 pairing.lock）的規則要跟外殼啟動引擎的規則（installs.Layout.Server）一致：
// 官網版解除安裝會保留 ~/.local/share/zuin/data，之後改裝系統套件時，引擎用的是 /usr/share/0c/data。以前 0c-update
// 只要家目錄有字典就用它，models 拿舊字典算雜湊、讀不到（或讀錯）pairing.lock，回報 dictionary_mismatch 或反覆重新配對。
func TestActiveDataFollowsEngine(t *testing.T) {
	root := t.TempDir()
	l := installs.Layout{Home: filepath.Join(root, "home"), Libexec: filepath.Join(root, "usr/libexec/0c"),
		Data: filepath.Join(root, "usr/share/0c"), Bin: filepath.Join(root, "usr/bin")}
	base := filepath.Join(l.Home, ".local/share/zuin")
	personal := filepath.Join(base, "data")
	writeManifest(t, personal, `{"dictionary":"old"}`)
	writeManifest(t, l.SystemData(), `{"dictionary":"system"}`)

	// 沒有任何安裝（開發、測試）：沿用以前的找法
	if _, ok := activeDataIn(l); ok {
		t.Fatal("沒有安裝時不決定")
	}
	// 只有系統安裝（官網版已解除安裝，留下字典）：用系統的字典，與引擎相同
	writeExe(t, filepath.Join(l.Libexec, "0c-server"))
	if server, args := l.Server(); server != filepath.Join(l.Libexec, "0c-server") || len(args) != 2 || args[1] != l.SystemData() {
		t.Fatalf("引擎 %s %v", server, args)
	}
	if data, ok := activeDataIn(l); !ok || data != l.SystemData() {
		t.Fatalf("data %q ok %v", data, ok)
	}
	// 官網安裝的引擎也在：官網安裝優先，用它的字典；核心更新後是版本目錄裡的配對字典（engine.json）
	writeExe(t, filepath.Join(l.Home, ".local/bin/0c-server"))
	if data, ok := activeDataIn(l); !ok || data != personal {
		t.Fatalf("data %q ok %v", data, ok)
	}
	version := filepath.Join(base, "versions", "0.2.3-c-abc")
	writeExe(t, filepath.Join(version, "0c-server"))
	writeManifest(t, filepath.Join(version, "data"), `{"dictionary":"core"}`)
	if err := enginedir.Write(base, enginedir.Current{Directory: "versions/0.2.3-c-abc", Version: "0.2.3"}); err != nil {
		t.Fatal(err)
	}
	if data, ok := activeDataIn(l); !ok || data != filepath.Join(version, "data") {
		t.Fatalf("data %q ok %v", data, ok)
	}
	// 官網版解除安裝（引擎移除、engine.json 與版本目錄留著）：回到系統安裝的字典，不跟著殘留的指標走
	os.Remove(filepath.Join(l.Home, ".local/bin/0c-server"))
	if data, ok := activeDataIn(l); !ok || data != l.SystemData() {
		t.Fatalf("data %q ok %v", data, ok)
	}
}

// dataPackDir（models、vocab 共用）照著用：官網版解除安裝後留下字典與 engine.json，引擎是系統安裝的。
func TestDataPackDirAfterPersonalUninstall(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	defer func(l, d, b string) { installs.LibexecDir, installs.DataDir, installs.BinDir = l, d, b }(installs.LibexecDir, installs.DataDir, installs.BinDir)
	installs.LibexecDir, installs.DataDir, installs.BinDir = filepath.Join(root, "usr/libexec/0c"), filepath.Join(root, "usr/share/0c"), filepath.Join(root, "usr/bin")
	base := filepath.Join(home, ".local/share/zuin")
	writeManifest(t, filepath.Join(base, "data"), `{"dictionary":"old"}`)
	version := filepath.Join(base, "versions", "0.2.2-c-abc")
	writeExe(t, filepath.Join(version, "0c-server"))
	writeManifest(t, filepath.Join(version, "data"), `{"dictionary":"core"}`)
	if err := enginedir.Write(base, enginedir.Current{Directory: "versions/0.2.2-c-abc", Version: "0.2.2"}); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(installs.DataDir, "data")
	writeManifest(t, system, `{"dictionary":"system"}`)
	writeExe(t, filepath.Join(installs.LibexecDir, "0c-server"))
	if got := dataPackDir(base); got != system {
		t.Fatalf("dataPackDir = %s，引擎用的是 %s", got, system)
	}
}
