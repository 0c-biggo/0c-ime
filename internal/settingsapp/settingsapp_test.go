//go:build linux

package settingsapp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// program 在 dir 寫一個可執行的假設定程式（殼腳本），回傳路徑與雜湊。
func program(t *testing.T, dir, name, body string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sum, err := update.Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, sum
}

// coreDir 建立 base 下的核心版本目錄並讓引擎指標指向它。
func coreDir(t *testing.T, base string) string {
	t.Helper()
	rel := "versions/0.1.0-dev.43-c-aaaaaaaaaaaa"
	if err := enginedir.Write(base, enginedir.Current{Directory: rel, Version: "0.1.0-dev.43"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, rel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// sessionOf 讀 /proc/<pid>/stat 的 session 編號（第 6 欄；行程名稱可能含空白，所以從最後一個 ")" 往後切）。
func sessionOf(t *testing.T, pid int) int {
	t.Helper()
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	rest := strings.Fields(string(raw)[strings.LastIndex(string(raw), ")")+1:])
	sid, err := strconv.Atoi(rest[3]) // state ppid pgrp session
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestPageWhitelist(t *testing.T) {
	for in, want := range map[string]string{"update": "update", "input": "input", "mode": "mode", "candidates": "candidates", "keyboard": "keyboard", "vocab": "vocab", "models": "models", "": "overview", "account": "overview", "../x": "overview", "UPDATE": "overview"} {
		if got := Page(in); got != want {
			t.Errorf("Page(%q) = %q，應為 %q", in, got, want)
		}
	}
}

func TestLocatePrefersTheCurrentCoreVersionDirectory(t *testing.T) {
	base := t.TempDir()
	dir := coreDir(t, base)
	core, sum := program(t, dir, "0c-settings", "exit 0")
	if err := enginedir.WriteFiles(dir, map[string]string{"0c-settings": sum}); err != nil {
		t.Fatal(err)
	}
	installed, installedSum := program(t, filepath.Join(t.TempDir(), "bin"), "0c-settings", "exit 0 # older copy")
	loc, err := Locate(base, installed, installedSum)
	if err != nil || loc.Path != core || loc.Source != SourceCore {
		t.Fatalf("核心更新帶來的較新版本優先：%+v %v", loc, err)
	}
}

func TestLocateFallsBackToTheInstalledCopy(t *testing.T) {
	installed, sum := program(t, filepath.Join(t.TempDir(), "bin"), "0c-settings", "exit 0")
	// 沒有引擎指標（沒做過核心更新）。
	if loc, err := Locate(t.TempDir(), installed, sum); err != nil || loc.Path != installed || loc.Source != SourceInstall {
		t.Fatalf("%+v %v", loc, err)
	}
	// 指標指向的版本目錄沒有設定程式（舊版核心更新準備的目錄）。
	base := t.TempDir()
	coreDir(t, base)
	if loc, err := Locate(base, installed, sum); err != nil || loc.Source != SourceInstall {
		t.Fatalf("%+v %v", loc, err)
	}
}

func TestLocateRefusesUnverifiableFilesAndFallsBack(t *testing.T) {
	installed, installedSum := program(t, filepath.Join(t.TempDir(), "bin"), "0c-settings", "exit 0")
	for name, setup := range map[string]func(dir, path, sum string){
		"雜湊與記錄不符": func(dir, path, sum string) {
			enginedir.WriteFiles(dir, map[string]string{"0c-settings": strings.Repeat("0", 64)})
		},
		"沒有雜湊記錄": func(dir, path, sum string) {},
		"群組可寫": func(dir, path, sum string) {
			enginedir.WriteFiles(dir, map[string]string{"0c-settings": sum})
			os.Chmod(path, 0o770)
		},
		"沒有執行權限": func(dir, path, sum string) {
			enginedir.WriteFiles(dir, map[string]string{"0c-settings": sum})
			os.Chmod(path, 0o600)
		},
		"內容被替換": func(dir, path, sum string) {
			enginedir.WriteFiles(dir, map[string]string{"0c-settings": sum})
			os.WriteFile(path, []byte("#!/bin/sh\nrm -rf x\n"), 0o700)
		},
	} {
		base := t.TempDir()
		dir := coreDir(t, base)
		path, sum := program(t, dir, "0c-settings", "exit 0")
		setup(dir, path, sum)
		loc, err := Locate(base, installed, installedSum)
		if err != nil || loc.Source != SourceInstall {
			t.Errorf("%s：核心目錄的檔案核對不過時要退回安裝位置的副本：%+v %v", name, loc, err)
		}
		// 兩邊都不可用：回報原因，而不是啟動它。
		if _, err = Locate(base, "", ""); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%s：應說明核對失敗的原因：%v", name, err)
		}
	}
}

func TestLocateRefusesSymlinks(t *testing.T) {
	base := t.TempDir()
	dir := coreDir(t, base)
	real, sum := program(t, t.TempDir(), "elsewhere", "exit 0")
	if err := os.Symlink(real, filepath.Join(dir, "0c-settings")); err != nil {
		t.Fatal(err)
	}
	enginedir.WriteFiles(dir, map[string]string{"0c-settings": sum})
	if _, err := Locate(base, "", ""); err == nil || !strings.Contains(err.Error(), "不是一般檔案") {
		t.Fatalf("符號連結不得啟動：%v", err)
	}
}

func TestLocateReportsNotFoundWhenThereIsNoSettingsProgram(t *testing.T) {
	if _, err := Locate(t.TempDir(), "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := Locate(t.TempDir(), filepath.Join(t.TempDir(), "absent"), "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestLaunchPassesPageAndUpdaterAndDoesNotWaitForTheProgram(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "seen")
	path, _ := program(t, dir, "0c-settings", `echo "$@|$ZC_UPDATER" > `+out)
	if err := Launch(path, LaunchOptions{Page: "update", Updater: "/stable/0c-update", Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != "--page update|/stable/0c-update" {
		t.Fatalf("%q", b)
	}
	// 不認得的頁面開總覽，而不是把任意字串傳給程式。
	if err := Launch(path, LaunchOptions{Page: "--evil", Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != "--page overview|" {
		t.Fatalf("%q", b)
	}
}

// 還在執行就算啟動成功，且不等到它結束；它自成一個工作階段，不隨呼叫端結束。
func TestLaunchReturnsWhileTheProgramKeepsRunningInItsOwnSession(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	path, _ := program(t, dir, "0c-settings", `echo $$ > `+pidFile+"\nexec sleep 30")
	start := time.Now()
	if err := Launch(path, LaunchOptions{Wait: 300 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("不該等到程式結束")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	if sid := sessionOf(t, pid); sid != pid {
		t.Fatalf("設定程式應自成一個工作階段（sid=%d pid=%d）", sid, pid)
	}
}

func TestLaunchReportsAnImmediateCrashWithItsOutput(t *testing.T) {
	dir := t.TempDir()
	path, _ := program(t, dir, "0c-settings", `echo "libwebkit2gtk-4.1.so.0: cannot open shared object file" >&2; exit 127`)
	err := Launch(path, LaunchOptions{Wait: 2 * time.Second, Log: filepath.Join(dir, "last.log")})
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 127 || !strings.Contains(exit.Tail, "libwebkit2gtk-4.1.so.0") || !strings.Contains(err.Error(), "127") {
		t.Fatalf("%v", err)
	}
	// 單一實例交棒給既有視窗：結束碼 0 不是失敗。
	handoff, _ := program(t, dir, "0c-settings-2", "exit 0")
	if err := Launch(handoff, LaunchOptions{Wait: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
}

func TestParseMissingLibraries(t *testing.T) {
	ldd := "\tlinux-vdso.so.1 (0x00007ffd)\n\tlibwebkit2gtk-4.1.so.0 => not found\n\tlibgtk-3.so.0 => /lib/x86_64-linux-gnu/libgtk-3.so.0 (0x1)\n\tlibjavascriptcoregtk-4.1.so.0 => not found\n"
	got := parseMissing(ldd)
	if len(got) != 2 || got[0] != "libwebkit2gtk-4.1.so.0" || got[1] != "libjavascriptcoregtk-4.1.so.0" {
		t.Fatalf("%v", got)
	}
	if parseMissing("\tlibc.so.6 => /lib/libc.so.6 (0x1)\n") != nil {
		t.Fatal("全部找得到時不該回報")
	}
	// 對真實的二進位：/bin/sh 的函式庫都在。
	if missing := MissingLibraries("/bin/sh"); len(missing) != 0 {
		t.Fatalf("%v", missing)
	}
}

func TestDesktopEntryIsCreatedUpdatedAndRemovedOnlyWhenOurs(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "applications", DesktopFile)
	changed, err := EnsureDesktopEntry(data, "/home/u/.local/share/zuin/bin/0c-update")
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"[Desktop Entry]", `Exec="/home/u/.local/share/zuin/bin/0c-update" open-settings`, "Name=靈犀注音設定", "Terminal=false",
		"Icon=0c-settings", "StartupWMClass=0c-settings", desktopMarker} {
		if !strings.Contains(string(b), want) {
			t.Errorf("缺少 %s：%s", want, b)
		}
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("權限 %v", st.Mode().Perm())
	}
	iconFile := filepath.Join(data, iconPath)
	if got, err := os.ReadFile(iconFile); err != nil || !bytes.Equal(got, icon) || len(icon) < 1000 {
		t.Fatalf("應一併寫入品牌圖示：%v", err)
	}
	if changed, err = EnsureDesktopEntry(data, "/home/u/.local/share/zuin/bin/0c-update"); err != nil || changed {
		t.Fatalf("內容沒變不該重寫：%v %v", changed, err)
	}
	// 圖示被刪掉或損壞時補回。
	os.WriteFile(iconFile, []byte("broken"), 0o644)
	if changed, err = EnsureDesktopEntry(data, "/home/u/.local/share/zuin/bin/0c-update"); err != nil || !changed {
		t.Fatalf("圖示不對要補回：%v %v", changed, err)
	}
	if got, _ := os.ReadFile(iconFile); !bytes.Equal(got, icon) {
		t.Fatal("圖示沒有補回")
	}
	if changed, err = EnsureDesktopEntry(data, "/home/u/.local/bin/0c-update"); err != nil || !changed {
		t.Fatalf("路徑變了要更新：%v %v", changed, err)
	}
	if err = RemoveDesktopEntry(data); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("應已移除")
	}
	if _, err = os.Stat(iconFile); !os.IsNotExist(err) {
		t.Fatal("我們寫的圖示也要一併移除")
	}
	// 使用者自己建立的同名檔案：不覆寫、不移除。
	os.WriteFile(path, []byte("[Desktop Entry]\nName=mine\n"), 0o644)
	if _, err = EnsureDesktopEntry(data, "/x/0c-update"); err == nil {
		t.Fatal("不得覆寫別人的檔案")
	}
	if err = RemoveDesktopEntry(data); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "mine") {
		t.Fatal("不得移除別人的檔案")
	}
	if _, err := os.Stat(iconFile); err == nil {
		t.Fatal("別人的項目不動，也不該為它寫入圖示")
	}
}

// 內嵌的圖示必須和設定程式用的是同一張（兩處各存一份，免得改了一邊）。
func TestEmbeddedIconMatchesTheSettingsProgramIcon(t *testing.T) {
	want, err := os.ReadFile("../../platforms/settings/icons/128x128.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, want) {
		t.Fatal("internal/settingsapp/icon.png 與 platforms/settings/icons/128x128.png 不一致")
	}
}

func TestDesktopEntryRefusesPathsThatCouldChangeTheCommand(t *testing.T) {
	for _, bad := range []string{"relative/0c-update", `/a"b/0c-update`, "/a`b/0c-update", "/a$HOME/0c-update", `/a\b/0c-update`, "/a%f/0c-update", "/a\nExec=evil/0c-update"} {
		if _, err := EnsureDesktopEntry(t.TempDir(), bad); err == nil {
			t.Errorf("應拒絕 %q", bad)
		}
	}
	// 含空白的路徑在引號內是安全的。
	if _, err := EnsureDesktopEntry(t.TempDir(), "/home/a b/0c-update"); err != nil {
		t.Fatal(err)
	}
}

// 系統套件的設定程式（#181）沒有收據雜湊，改核對擁有者與權限。
func TestLocateSystemChecksOwnerAndPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0c-settings")
	if _, err := LocateSystem(path); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
	if _, err := LocateSystem(path); err == nil {
		t.Fatal("a file owned by the test user is not a system file")
	}
	old := systemOwner
	systemOwner = os.Getuid()
	t.Cleanup(func() { systemOwner = old })
	loc, err := LocateSystem(path)
	if err != nil || loc.Path != path || loc.Source != SourceSystem {
		t.Fatalf("got %+v %v", loc, err)
	}
	os.Chmod(path, 0o775)
	if _, err := LocateSystem(path); err == nil {
		t.Fatal("group-writable must be refused")
	}
	os.Chmod(path, 0o755)
	link := filepath.Join(dir, "link")
	os.Symlink(path, link)
	if _, err := LocateSystem(link); err == nil {
		t.Fatal("symlink must be refused")
	}
}
