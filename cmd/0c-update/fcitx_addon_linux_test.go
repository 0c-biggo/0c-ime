//go:build !windows && !darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestObsoleteAddonDirsOnlyMatchesWhatOldInstallersWrote(t *testing.T) {
	home := "/home/u"
	for _, value := range []string{
		"${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5", // dev.59 以前
		"/home/u/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5", // environment.d 展開後
		"${HOME}/.local/lib/fcitx5:/usr/lib64/fcitx5",                // #92 使用者改成 Fedora 路徑
		`"$HOME/.local/lib/fcitx5:/usr/lib/fcitx5/"`,
	} {
		if !obsoleteAddonDirs(value, home) {
			t.Errorf("舊版路徑沒認出來：%s", value)
		}
	}
	for _, value := range []string{"", "/usr/lib64/fcitx5", "${HOME}/.local/lib/fcitx5:/opt/addons",
		"/home/other/.local/lib/fcitx5:/usr/lib/fcitx5", "/home/u/src/fcitx5:/usr/lib/fcitx5"} {
		if obsoleteAddonDirs(value, home) {
			t.Errorf("使用者自己的路徑不能當成舊版的：%s", value)
		}
	}
}

func TestRestartEnvDropsOldAddonDirsOnlyForAbsoluteLibrary(t *testing.T) {
	home := t.TempDir()
	env := []string{"DISPLAY=:0", "FCITX_ADDON_DIRS=" + home + "/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5"}
	// 沒有 addon 設定或是舊版的相對 Library（回復到 dev.59 以前）：照舊沿用。
	if got := fcitxRestartEnv(env, home); !slices.Equal(got, env) {
		t.Fatalf("沒有新版設定時不應更動：%v", got)
	}
	conf := filepath.Join(home, fcitxAddonConf)
	os.MkdirAll(filepath.Dir(conf), 0o755)
	os.WriteFile(conf, []byte("[Addon]\nType=SharedLibrary\nLibrary=lib0c\n"), 0o644)
	if got := fcitxRestartEnv(env, home); !slices.Equal(got, env) {
		t.Fatalf("相對 Library 仍需要搜尋路徑：%v", got)
	}
	os.WriteFile(conf, []byte("[Addon]\nType=SharedLibrary\nLibrary="+home+"/.local/lib/fcitx5/lib0c\n"), 0o644)
	if got := fcitxRestartEnv(env, home); !slices.Equal(got, env[:1]) {
		t.Fatalf("新版外殼不需要舊版搜尋路徑：%v", got)
	}
	own := []string{"DISPLAY=:0", "FCITX_ADDON_DIRS=/opt/addons:/usr/lib64/fcitx5"}
	if got := fcitxRestartEnv(own, home); !slices.Equal(got, own) {
		t.Fatalf("使用者自己的搜尋路徑要保留：%v", got)
	}
}

func TestShellLoadedReadsTheRunningFcitxMappings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	library := filepath.Join(home, fcitxShellLib)
	os.MkdirAll(filepath.Dir(library), 0o755)
	os.WriteFile(library, []byte("shell"), 0o644)
	var stat syscall.Stat_t
	if err := syscall.Stat(library, &stat); err != nil {
		t.Fatal(err)
	}
	fakeProc(t, map[int][3]any{49: {"fcitx5", "S", 1}})
	maps := filepath.Join(procRoot, "49", "maps")
	os.WriteFile(maps, []byte("7f00-7f10 r-xp 00000000 08:01 1 /usr/lib64/libFcitx5Core.so.7\n"), 0o644)
	if shellLoaded(fcitxShellLib, legacyShellLib) {
		t.Fatal("外殼沒載入卻回報已載入")
	}
	line := fmt.Sprintf("7f00-7f10 r-xp 00000000 %02x:%02x %d %s\n", unix.Major(stat.Dev), unix.Minor(stat.Dev), stat.Ino, library)
	os.WriteFile(maps, []byte(line), 0o644)
	if !shellLoaded(fcitxShellLib) {
		t.Fatal("已載入的外殼沒認出來")
	}
	// 核對新外殼生效時，被取代的舊檔不算；判斷是否仍在使用則要算（shellMapped）。
	os.WriteFile(maps, []byte(strings.TrimSuffix(line, "\n")+" (deleted)\n"), 0o644)
	if shellLoaded(fcitxShellLib) || !shellMapped() {
		t.Fatal("已被取代的舊外殼：不是目前的檔案，但仍在使用")
	}
}

// 上次更新換了 lib0c.so 但 Fcitx5 沒重新啟動時，使用者還在用舊外殼打字，不能當成沒在用而跳過組字保護。
func TestShellMappedCountsReplacedAndLegacyShellsAsInUse(t *testing.T) {
	fakeProc(t, map[int][3]any{49: {"fcitx5", "S", 1}})
	maps := filepath.Join(procRoot, "49", "maps")
	core := "7f00-7f10 r-xp 00000000 08:01 7 /usr/lib64/libFcitx5Core.so.7\n"
	for _, c := range []struct {
		name, maps string
		mapped     bool
	}{
		{"目前路徑", "7f20-7f30 r-xp 00000000 08:02 1234 /home/u/.local/lib/fcitx5/lib0c.so\n", true},
		{"已被取代", "7f20-7f30 r-xp 00000000 08:02 1234 /home/u/.local/lib/fcitx5/lib0c.so (deleted)\n", true},
		{"別的 inode", "7f20-7f30 r-xp 00000000 08:02 99 /var/home/u/.local/lib/fcitx5/lib0c.so\n", true},
		{"dev.15 以前的 libzuin.so", "7f20-7f30 r-xp 00000000 08:02 5 /home/u/.local/lib/fcitx5/libzuin.so (deleted)\n", true},
		{"家目錄含空白", "7f20-7f30 r-xp 00000000 08:02 5 /home/u v/.local/lib/fcitx5/lib0c.so\n", true},
		{"沒有外殼（#93）", "7f20-7f30 r-xp 00000000 08:02 6 /usr/lib64/fcitx5/libdbus.so\n", false},
		{"名字相近", "7f20-7f30 r-xp 00000000 08:02 6 /opt/x/lib0c.so.bak\n", false},
	} {
		os.WriteFile(maps, []byte(core+c.maps), 0o644)
		if got := shellMapped(); got != c.mapped {
			t.Errorf("%s：shellMapped() = %v，預期 %v", c.name, got, c.mapped)
		}
	}
	os.Remove(maps)
	if !shellMapped() {
		t.Error("讀不到 maps 時要當成使用中")
	}
}
