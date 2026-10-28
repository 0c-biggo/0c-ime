//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// x11Desktop 在 fakeDesktop 上換成 Debian 系 X11 桌面：有 ~/.xsessionrc 機制，ibus-daemon 不是 systemd
// 服務，而是桌面依 argv 啟動的行程（pid 4242）。argv[0] 寫 "@fake" 時換成假 ibus-daemon 的絕對路徑；
// 假 ibus-daemon 另外記下拿到的環境。
func x11Desktop(t *testing.T, desktop string, argv ...string) (home, log string) {
	t.Helper()
	home, log = fakeDesktop(t)
	t.Setenv("XDG_CURRENT_DESKTOP", desktop)
	dir := os.Getenv("PATH")
	hook := filepath.Join(etcDir, "X11/Xsession.d/40x11-common_xsessionrc")
	os.MkdirAll(filepath.Dir(hook), 0o755)
	os.WriteFile(hook, []byte("# Debian\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pgrep"), []byte("#!/bin/sh\necho \"pgrep $*\" >> "+log+"\necho 4242\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "ibus-daemon"), []byte("#!/bin/sh\necho \"ibus-daemon $*\" >> "+log+
		"\necho \"daemon-env IBUS_COMPONENT_PATH=$IBUS_COMPONENT_PATH DISPLAY=$DISPLAY\" >> "+log+"\n"), 0o755)
	old := procDir
	procDir = t.TempDir()
	t.Cleanup(func() { procDir = old })
	os.MkdirAll(filepath.Join(procDir, "4242"), 0o755)
	if len(argv) > 0 && argv[0] == "@fake" {
		argv[0] = filepath.Join(dir, "ibus-daemon")
	}
	os.WriteFile(filepath.Join(procDir, "4242/cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644)
	os.WriteFile(filepath.Join(procDir, "4242/environ"), []byte("DISPLAY=:0\x00IBUS_COMPONENT_PATH=/old\x00"), 0o644)
	return home, log
}

func sources(t *testing.T, log, schema string) string {
	t.Helper()
	b, err := os.ReadFile(log + "." + schema)
	if err != nil {
		return "[('xkb', 'us')]"
	}
	return strings.TrimSpace(string(b))
}

// #237：Linux Mint Cinnamon（X11）上 register 要加 Cinnamon 的輸入來源、寫 ~/.xsessionrc，並以原本的參數
// 重新啟動桌面起的 ibus-daemon（Cinnamon 自己當面板，不能換成 IBus 的面板）。
func TestCinnamonRegister(t *testing.T) {
	home, log := x11Desktop(t, "X-Cinnamon", "@fake", "--panel", "disable", "--xim")
	cache := filepath.Join(home, registryCache)
	os.MkdirAll(filepath.Dir(cache), 0o755)
	os.WriteFile(cache, []byte("stale"), 0o644)
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if want := "ibus-daemon --panel disable --xim --replace --daemonize --cache=refresh\n"; !strings.Contains(got, want) {
		t.Errorf("沒有以原本的參數重新啟動 ibus-daemon：\n%s", got)
	}
	if want := "daemon-env IBUS_COMPONENT_PATH=" + componentPath(home) + " DISPLAY=:0\n"; !strings.Contains(got, want) {
		t.Errorf("新的 ibus-daemon 要沿用原本的環境、只換元件路徑：\n%s", got)
	}
	if got := sources(t, log, cinnamonSources); got != "[('xkb', 'us'), ('ibus', '0c')]" {
		t.Errorf("Cinnamon 輸入來源 = %s", got)
	}
	if got := sources(t, log, gnomeSources); got != "[('xkb', 'us')]" {
		t.Errorf("不該動 GNOME 的輸入來源：%s", got)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("登錄快取沒有刪掉")
	}
	if rc := setting(t, home, sessionRC); !strings.Contains(rc, sessionBegin) || !strings.Contains(rc, sessionEnd) {
		t.Errorf("~/.xsessionrc = %q", rc)
	}

	// 重跑安裝：沒有變更時不重啟 IBus、不加回使用者移掉的輸入來源。
	os.WriteFile(log+"."+cinnamonSources, []byte("[('xkb', 'us')]\n"), 0o644)
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); strings.Contains(got, "ibus-daemon") || strings.Contains(got, "gsettings set") {
		t.Errorf("已登錄時不該重啟或改輸入來源：\n%s", got)
	}

	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	got = calls(t, log)
	if !strings.Contains(got, "ibus-daemon --panel disable --xim --replace --daemonize --cache=refresh\n") ||
		!strings.Contains(got, "daemon-env IBUS_COMPONENT_PATH= DISPLAY=:0\n") {
		t.Errorf("移除後要以不含使用者目錄的環境重新啟動 ibus-daemon：\n%s", got)
	}
	if rc := setting(t, home, sessionRC); rc != "（沒有檔案）" {
		t.Errorf("只有我們那一段的 ~/.xsessionrc 要刪掉：%q", rc)
	}
}

func TestCinnamonUnregisterRemovesSource(t *testing.T) {
	// im-config 起的 daemon；上一次登錄重新啟動過的參數不再重複疊上去。argv[0] 不是絕對路徑時從 PATH 找。
	_, log := x11Desktop(t, "X-Cinnamon", "ibus-daemon", "--daemonize", "--xim", "--replace", "-t", "auto", "--cache=refresh")
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); !strings.Contains(got, "ibus-daemon --xim --replace --daemonize --cache=refresh\n") {
		t.Errorf("沒有重新啟動 ibus-daemon：\n%s", got)
	}
	os.WriteFile(log+"."+cinnamonSources, []byte("[('xkb', 'tw'), ('ibus', '0c'), ('ibus', 'chewing')]\n"), 0o644)
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	if got := sources(t, log, cinnamonSources); got != "[('xkb', 'tw'), ('ibus', 'chewing')]" {
		t.Errorf("移除後 Cinnamon 輸入來源 = %s", got)
	}
}

// MATE、Xfce 用 IBus 自己的面板，沒有桌面的輸入來源清單：只寫元件路徑並重新啟動 daemon，不改任何清單。
func TestDesktopWithoutSourceList(t *testing.T) {
	home, log := x11Desktop(t, "MATE", "@fake", "--daemonize", "--xim")
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if strings.Contains(got, "gsettings set") {
		t.Errorf("MATE 沒有輸入來源清單，不該改 gsettings：\n%s", got)
	}
	if !strings.Contains(got, "--replace --daemonize --cache=refresh\n") {
		t.Errorf("沒有重新啟動 ibus-daemon：\n%s", got)
	}
	if !strings.Contains(setting(t, home, sessionRC), sessionBegin) {
		t.Error("沒有寫 ~/.xsessionrc")
	}
}

// GNOME 由 systemd 服務啟動 IBus，environment.d 就夠了：不寫 ~/.xsessionrc、不自己重啟 daemon。
func TestGNOMEKeepsSystemdPath(t *testing.T) {
	home, log := x11Desktop(t, "ubuntu:GNOME", "@fake", "--panel", "disable")
	dir := os.Getenv("PATH")
	os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\necho \"systemctl $*\" >> "+log+
		"\n[ \"$2\" = is-active ] && [ \"$4\" = org.freedesktop.IBus.session.GNOME.service ] && exit 0\n[ \"$2\" = is-active ] && exit 1\nexit 0\n"), 0o755)
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if !strings.Contains(got, "systemctl --user restart org.freedesktop.IBus.session.GNOME.service\n") || strings.Contains(got, "ibus-daemon ") {
		t.Errorf("GNOME 要重新啟動 systemd 服務，不自己啟動 daemon：\n%s", got)
	}
	if rc := setting(t, home, sessionRC); rc != "（沒有檔案）" {
		t.Errorf("GNOME 不該寫 ~/.xsessionrc：%q", rc)
	}
	if got := sources(t, log, gnomeSources); got != "[('xkb', 'us'), ('ibus', '0c')]" {
		t.Errorf("GNOME 輸入來源 = %s", got)
	}
}

// 0.2.6 在 Cinnamon 登錄過（只有 environment.d，沒有輸入來源）：更新後的 toolkits 補上 ~/.xsessionrc、
// 刪快取、加輸入來源，但不重啟 IBus；第二次什麼都不做。
func TestToolkitsRepairsOldX11Registration(t *testing.T) {
	home, log := x11Desktop(t, "X-Cinnamon", "@fake", "--daemonize", "--xim")
	os.MkdirAll(filepath.Dir(filepath.Join(home, componentFile)), 0o755)
	os.WriteFile(filepath.Join(home, componentFile), []byte("<name>"+componentName+"</name>"), 0o644)
	cache := filepath.Join(home, registryCache)
	os.MkdirAll(filepath.Dir(cache), 0o755)
	os.WriteFile(cache, []byte("stale"), 0o644)
	if err := syncRegisteredToolkits(); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if strings.Contains(got, "ibus-daemon ") || strings.Contains(got, "restart") {
		t.Errorf("更新器補設定時不重啟 IBus：\n%s", got)
	}
	if !strings.Contains(setting(t, home, sessionRC), sessionBegin) {
		t.Error("沒有補上 ~/.xsessionrc")
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("沒有刪掉不含使用者目錄的登錄快取")
	}
	if got := sources(t, log, cinnamonSources); got != "[('xkb', 'us'), ('ibus', '0c')]" {
		t.Errorf("Cinnamon 輸入來源 = %s", got)
	}
	os.WriteFile(log+"."+cinnamonSources, []byte("[('xkb', 'us')]\n"), 0o644)
	if err := syncRegisteredToolkits(); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); strings.Contains(got, "gsettings set") {
		t.Errorf("已補過就不再加輸入來源：\n%s", got)
	}
}

// ~/.xsessionrc 由 Xsession 以 sh -e 讀入：我們那一段在各種情況下都要成功，而且只補上缺的目錄。
func TestSessionRCIsSafeShell(t *testing.T) {
	home, _ := x11Desktop(t, "X-Cinnamon")
	rc := filepath.Join(home, sessionRC)
	os.WriteFile(rc, []byte("export FOO=bar"), 0o600) // 使用者自己的內容，沒有結尾換行
	if _, err := syncSessionRC(home, true); err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(home, ".local/share/ibus/component")
	for _, c := range []struct{ before, after string }{
		{"", "/usr/share/ibus/component:" + user},
		{"/opt/ibus", "/opt/ibus:" + user},
		{"/usr/share/ibus/component:" + user, "/usr/share/ibus/component:" + user},
	} {
		cmd := exec.Command("/bin/sh", "-e", "-c", `. "$HOME/.xsessionrc"; echo "$FOO $IBUS_COMPONENT_PATH"`)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		if c.before != "" {
			cmd.Env = append(cmd.Env, "IBUS_COMPONENT_PATH="+c.before)
		}
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != "bar "+c.after {
			t.Errorf("IBUS_COMPONENT_PATH=%q 讀入後 = %q（%v），預期 %q", c.before, out, err, c.after)
		}
	}
	if info, _ := os.Stat(rc); info.Mode().Perm() != 0o600 {
		t.Errorf("權限被改成 %v", info.Mode().Perm())
	}
	// 再寫一次不重複；移除只拿掉我們那一段。
	if changed, _ := syncSessionRC(home, true); changed {
		t.Error("內容相同時不該改寫")
	}
	if _, err := syncSessionRC(home, false); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, home, sessionRC); got != "export FOO=bar\n" {
		t.Errorf("移除後 = %q", got)
	}
	// 連結到別處的設定檔：改連結指向的檔案，連結保留。
	real := filepath.Join(home, "dotfiles-xsessionrc")
	os.WriteFile(real, []byte("# mine\n"), 0o644)
	os.Remove(rc)
	os.Symlink(real, rc)
	if _, err := syncSessionRC(home, true); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(rc); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("連結被換成一般檔案")
	}
	if b, _ := os.ReadFile(real); !strings.HasPrefix(string(b), "# mine\n"+sessionBegin) {
		t.Errorf("連結指向的檔案 = %q", b)
	}
}

// 安裝器解除安裝時（scripts/install_package.py 的 remove_session_block）用同一組標記找我們那一段。
func TestInstallerUsesSameSessionMarkers(t *testing.T) {
	b, err := os.ReadFile("../../scripts/install_package.py")
	if err != nil {
		t.Fatal(err)
	}
	py := string(b)
	begin := strings.SplitN(sessionBegin, "：", 2)[0] + "："
	if !strings.Contains(py, `IBUS_SESSION_BEGIN = "`+begin+`"`) || !strings.Contains(py, `IBUS_SESSION_END = "`+sessionEnd+`"`) {
		t.Errorf("install_package.py 的 ~/.xsessionrc 標記與 %q／%q 不同", begin, sessionEnd)
	}
	if !strings.Contains(py, `"`+cinnamonSources+`"`) || !strings.Contains(py, `"`+gnomeSources+`"`) {
		t.Error("install_package.py 沒有移除 GNOME／Cinnamon 兩個輸入來源清單")
	}
}
