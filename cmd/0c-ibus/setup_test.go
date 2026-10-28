//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDesktop 把 PATH 換成只會記錄參數的假工具，環境設成 GNOME 的預設，回傳家目錄與紀錄檔。
func fakeDesktop(t *testing.T) (home, log string) {
	t.Helper()
	home, dir, log := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "calls")
	for _, n := range []string{"ibus-daemon", "systemctl", "gsettings", "localectl", "pgrep"} {
		script := "#!/bin/sh\necho \"" + n + " $*\" >> " + log + "\n"
		switch n {
		case "gsettings":
			// 每個清單的值存在紀錄檔旁邊；沒設定過時是 [('xkb', 'us')]。
			// PATH 只有假工具，只能用 shell 內建指令。
			script += "f=\"" + log + ".$2\"\nv=\"[('xkb', 'us')]\"\n[ -f \"$f\" ] && IFS= read -r v < \"$f\"\n" +
				"[ \"$1\" = get ] && echo \"$v\"\n[ \"$1\" = set ] && echo \"$4\" > \"$f\"\nexit 0\n"
		case "systemctl":
			script += "[ \"$2\" = is-active ] && exit 1\nexit 0\n" // 沒有執行中的 IBus 服務
		case "pgrep":
			script += "exit 1\n"
		}
		os.WriteFile(filepath.Join(dir, n), []byte(script), 0o755)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	// gnome-session 的預設不算使用者自訂。
	t.Setenv("GTK_IM_MODULE", "")
	t.Setenv("QT_IM_MODULE", "ibus")
	t.Setenv("QT_IM_MODULES", "wayland;ibus")
	old := etcDir
	etcDir = t.TempDir()
	t.Cleanup(func() { etcDir = old })
	return home, log
}

func calls(t *testing.T, log string) string {
	t.Helper()
	b, _ := os.ReadFile(log)
	os.WriteFile(log, nil, 0o644)
	return string(b)
}

func setting(t *testing.T, home, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, file))
	if os.IsNotExist(err) {
		return "（沒有檔案）"
	}
	return string(b)
}

func TestRegisterWritesToolkitSettingsAndUnregisterClearsThem(t *testing.T) {
	home, log := fakeDesktop(t)
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, home, gtkModuleEnv); !strings.Contains(got, "\nGTK_IM_MODULE=ibus\n") {
		t.Errorf("GTK 設定 = %q", got)
	}
	// ibus 優先，載不到 IBus 外掛的 Qt 程式還能退回 Wayland 原生路徑。
	if got := setting(t, home, qtModuleEnv); !strings.Contains(got, "\nQT_IM_MODULES=ibus;wayland\n") {
		t.Errorf("Qt 設定 = %q", got)
	}
	got := calls(t, log)
	for _, want := range []string{"systemctl --user set-environment GTK_IM_MODULE=ibus", "systemctl --user set-environment QT_IM_MODULES=ibus;wayland",
		"gsettings set org.gnome.desktop.input-sources sources [('xkb', 'us'), ('ibus', '0c')]"} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("沒有呼叫 %q：\n%s", want, got)
		}
	}

	// 已登錄、後來的版本才加的設定：補上檔案，但不重啟 IBus、不改輸入來源；內容沒變時也不呼叫 systemctl。
	os.Remove(filepath.Join(home, qtModuleEnv))
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(setting(t, home, qtModuleEnv), "QT_IM_MODULES=") {
		t.Error("重跑 register 沒有補上 Qt 設定")
	}
	if got := calls(t, log); strings.Contains(got, "restart") || strings.Contains(got, "gsettings set") || strings.Contains(got, "GTK_IM_MODULE") {
		t.Errorf("只該補 Qt 設定：\n%s", got)
	}

	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{componentFile, componentEnv, gtkModuleEnv, qtModuleEnv} {
		if _, err := os.Stat(filepath.Join(home, file)); !os.IsNotExist(err) {
			t.Errorf("unregister 後 %s 還在（%v）", file, err)
		}
	}
	got = calls(t, log)
	for _, want := range []string{"systemctl --user unset-environment GTK_IM_MODULE", "systemctl --user unset-environment QT_IM_MODULES"} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("沒有呼叫 %q：\n%s", want, got)
		}
	}
}

func TestDisabledToolkitStaysDisabled(t *testing.T) {
	home, log := fakeDesktop(t)
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	calls(t, log)
	// 安裝後才停用：移掉設定並清環境變數，留下停用記號。
	if err := register(map[string]bool{"qt-module": false}); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, home, qtModuleEnv); strings.Contains(got, "QT_IM_MODULES=") || !strings.Contains(got, "已停用") {
		t.Errorf("停用後 Qt 設定 = %q", got)
	}
	if got := calls(t, log); !strings.Contains(got, "unset-environment QT_IM_MODULES\n") {
		t.Errorf("停用時沒有清 QT_IM_MODULES：\n%s", got)
	}
	// 重跑安裝、更新器補設定都不加回來；GTK 不受影響。
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	if err := syncRegisteredToolkits(); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, home, qtModuleEnv); strings.Contains(got, "QT_IM_MODULES=") {
		t.Errorf("停用的 Qt 設定被加回來：%q", got)
	}
	if !strings.Contains(setting(t, home, gtkModuleEnv), "GTK_IM_MODULE=ibus") {
		t.Error("停用 Qt 不該動到 GTK")
	}
	// 明確要求才恢復。
	if err := register(map[string]bool{"qt-module": true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(setting(t, home, qtModuleEnv), "QT_IM_MODULES=ibus;wayland") {
		t.Error("-qt-module=true 沒有恢復 Qt 設定")
	}
	// 停用記號也是我們的檔案：unregister 一併移除，但沒有設定可清。
	register(map[string]bool{"gtk-module": false})
	calls(t, log)
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, gtkModuleEnv)); !os.IsNotExist(err) {
		t.Error("unregister 沒有移除停用記號")
	}
	if got := calls(t, log); strings.Contains(got, "GTK_IM_MODULE") {
		t.Errorf("GTK 設定已停用，不該再清環境變數：\n%s", got)
	}
}

func TestUserToolkitSettingsWin(t *testing.T) {
	home, log := fakeDesktop(t)
	// 使用者在目前的環境指定 Fcitx5 的 GTK 模組：不寫 GTK 設定。
	t.Setenv("GTK_IM_MODULE", "fcitx")
	if err := register(nil); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, home, gtkModuleEnv); got != "（沒有檔案）" {
		t.Errorf("使用者的 GTK_IM_MODULE=fcitx 被蓋掉：%q", got)
	}
	if !strings.Contains(setting(t, home, qtModuleEnv), "QT_IM_MODULES=") {
		t.Error("沒有自訂的 Qt 仍要設定")
	}
	calls(t, log)

	// 之後使用者在自己的 environment.d 指定 Qt 5 的輸入法：移掉我們會蓋過它的 Qt 設定，但不清環境變數。
	user := filepath.Join(home, ".config/environment.d/50-im.conf")
	os.WriteFile(user, []byte("# mine\nQT_IM_MODULE=fcitx\n"), 0o644)
	if err := syncRegisteredToolkits(); err != nil {
		t.Fatal(err)
	}
	if got := setting(t, home, qtModuleEnv); got != "（沒有檔案）" {
		t.Errorf("使用者的 QT_IM_MODULE=fcitx 會被 QT_IM_MODULES 蓋掉：%q", got)
	}
	if got := calls(t, log); strings.Contains(got, "unset-environment") {
		t.Errorf("不該清使用者的環境變數：\n%s", got)
	}

	// 我們的設定生效中，使用者另外寫了自己的 QT_IM_MODULES：unregister 不清它。
	os.Remove(user)
	register(nil)
	os.WriteFile(filepath.Join(etcDir, "environment"), []byte("QT_IM_MODULES=\"fcitx;wayland\"\n"), 0o644)
	calls(t, log)
	if err := unregister(); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); strings.Contains(got, "unset-environment QT_IM_MODULES") {
		t.Errorf("清掉了 /etc/environment 的 QT_IM_MODULES：\n%s", got)
	}
}

func TestToolkitsSubcommandNeedsRegistration(t *testing.T) {
	home, _ := fakeDesktop(t)
	if err := syncRegisteredToolkits(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{gtkModuleEnv, qtModuleEnv} {
		if _, err := os.Stat(filepath.Join(home, file)); !os.IsNotExist(err) {
			t.Errorf("沒登錄就寫了 %s", file)
		}
	}
}
