//go:build linux

package main

// register／unregister：把 0c-ibus 接到目前使用者的 IBus，不需要 root。
//
// ibus-daemon 只讀 IBUS_COMPONENT_PATH（未設定時只有 /usr/share/ibus/component），而且優先使用
// ~/.cache/ibus/bus/registry 快取；快取只核對它當初看過的目錄，所以新增使用者目錄後要刪掉快取。
// GTK／Qt 改用 IBus 自己的輸入模組見 toolkit.go。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	componentFile = ".local/share/ibus/component/0c.xml"
	componentEnv  = ".config/environment.d/91-0c-ibus.conf"
	gtkModuleEnv  = ".config/environment.d/92-0c-ibus-gtk.conf"
	qtModuleEnv   = ".config/environment.d/93-0c-ibus-qt.conf"
	registryCache = ".cache/ibus/bus/registry"
	inputSource   = "('ibus', '" + engineName + "')"
)

func componentPath(home string) string {
	return "/usr/share/ibus/component:" + filepath.Join(home, ".local/share/ibus/component")
}

// writeFile 寫入內容不同的檔案；回報是否真的改了。
func writeFile(path, content string) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false, nil
	}
	return true, replaceFile(path, content)
}

func replaceFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func gnome() bool {
	return strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "GNOME")
}

// restartIBus 讓 ibus-daemon 以新的元件路徑重新讀取元件；沒有在執行時不啟動它。
// systemd 使用者管理員只在啟動時讀 environment.d，已在執行（例如 linger）時要另外更新。
func restartIBus(home string, registered bool) []string {
	var notes []string
	args := []string{"--user", "unset-environment", "IBUS_COMPONENT_PATH"}
	if registered {
		args = []string{"--user", "set-environment", "IBUS_COMPONENT_PATH=" + componentPath(home)}
	}
	if exec.Command("systemctl", args...).Run() != nil {
		notes = append(notes, "無法更新 systemd 使用者環境；重新登入後生效。")
	}
	for _, unit := range []string{"org.freedesktop.IBus.session.GNOME.service", "org.freedesktop.IBus.session.generic.service"} {
		if exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil {
			if err := exec.Command("systemctl", "--user", "restart", unit).Run(); err != nil {
				notes = append(notes, "無法重新啟動 IBus（"+err.Error()+"）；重新登入後生效。")
			}
			return notes
		}
	}
	if exec.Command("pgrep", "-u", fmt.Sprint(os.Getuid()), "-x", "ibus-daemon").Run() == nil {
		// 不是 systemd 啟動的 ibus-daemon 沿用自己的環境；重新登入後才讀得到使用者目錄。
		notes = append(notes, "IBus 不是由 systemd 啟動，重新登入後才會出現靈犀注音。")
	}
	return notes
}

func gsettingsSources() (string, error) {
	out, err := exec.Command("gsettings", "get", "org.gnome.desktop.input-sources", "sources").Output()
	return strings.TrimSpace(string(out)), err
}

var xkbName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// systemLayout 是系統的 X11 鍵盤配置（localectl），格式同 GNOME 的 xkb 來源 ID：配置或「配置+變體」。
func systemLayout() string {
	out, _ := exec.Command("localectl", "status").Output()
	layout, variant := "us", ""
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		v = strings.TrimSpace(strings.Split(v, ",")[0])
		switch {
		case !ok || !xkbName.MatchString(v):
		case k == "X11 Layout":
			layout = v
		case k == "X11 Variant":
			variant = v
		}
	}
	if variant != "" {
		return layout + "+" + variant
	}
	return layout
}

// withSource 把靈犀注音加到 GNOME 輸入來源清單最後；已經在清單裡就不動。清單是空的時候 GNOME
// 以系統鍵盤配置為唯一來源，先把它放進清單，才能切回原本的鍵盤。
func withSource(cur, layout string) string {
	if strings.Contains(cur, inputSource) {
		return cur
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(cur, "@a(ss) "), "["), "]"))
	if body == "" {
		return "[('xkb', '" + layout + "'), " + inputSource + "]"
	}
	return "[" + body + ", " + inputSource + "]"
}

func withoutSource(cur string) string {
	next := strings.ReplaceAll(cur, ", "+inputSource, "")
	next = strings.ReplaceAll(next, inputSource+", ", "")
	return strings.ReplaceAll(next, inputSource, "")
}

func setSources(edit func(string) string) error {
	cur, err := gsettingsSources()
	if err != nil {
		return err
	}
	if next := edit(cur); next != cur {
		return exec.Command("gsettings", "set", "org.gnome.desktop.input-sources", "sources", next).Run()
	}
	return nil
}

func register(want map[string]bool) error {
	home := os.Getenv("HOME")
	if home == "" {
		return errors.New("沒有 HOME")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("ibus-daemon"); err != nil {
		return errors.New("找不到 ibus-daemon；請先安裝 IBus")
	}
	_, statErr := os.Stat(filepath.Join(home, componentFile))
	registered := statErr == nil
	changed, err := writeFile(filepath.Join(home, componentFile), component(exe, home))
	if err != nil {
		return err
	}
	env := "# 讓 ibus-daemon 也讀使用者目錄的靈犀注音元件（系統元件仍在 /usr/share/ibus/component）。\n" +
		"IBUS_COMPONENT_PATH=/usr/share/ibus/component:${HOME}/.local/share/ibus/component\n"
	envChanged, err := writeFile(filepath.Join(home, componentEnv), env)
	if err != nil {
		return err
	}
	relogin, err := syncToolkits(home, want)
	if err != nil {
		return err
	}
	if registered && !changed && !envChanged {
		// 重跑安裝（修復排程）：已登錄且沒有變更時不重啟 IBus（會丟掉各程式正在組的字），
		// 也不把使用者自己移掉的輸入來源加回去。
		fmt.Println("靈犀注音已登錄在 IBus。")
		reloginNote(relogin)
		return nil
	}
	// 快取只核對當初看過的目錄，不刪掉就看不到新加入的使用者目錄。
	os.Remove(filepath.Join(home, registryCache))
	for _, n := range restartIBus(home, true) {
		fmt.Println(n)
	}
	if gnome() {
		if err := setSources(func(cur string) string { return withSource(cur, systemLayout()) }); err != nil {
			fmt.Println("無法加入 GNOME 輸入來源，請在「設定 → 鍵盤 → 輸入來源」加入「中文（靈犀注音）」。")
		}
	}
	fmt.Println("已登錄靈犀注音到 IBus。以 Super+空白鍵切換輸入來源。")
	reloginNote(relogin)
	return nil
}

// unregister 撤銷 register。與安裝器移除時的 ibus_unregister（scripts/install_package.py）做同樣的事：
// 只刪內容是靈犀注音的元件檔；GTK／Qt 的輸入設定只清我們設的（removeToolkits）。
func unregister() error {
	home := os.Getenv("HOME")
	component := filepath.Join(home, componentFile)
	if b, err := os.ReadFile(component); err == nil && !strings.Contains(string(b), "<name>"+componentName+"</name>") {
		return fmt.Errorf("%s 不是靈犀注音的元件檔，保留不動", component)
	}
	setSources(withoutSource) // 沒有桌面工作階段時失敗：檔案刪掉後下次登入就不會再出現
	if err := removeToolkits(home); err != nil {
		return err
	}
	for _, p := range []string{componentFile, componentEnv} {
		if err := os.Remove(filepath.Join(home, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	os.Remove(filepath.Join(home, registryCache))
	for _, n := range restartIBus(home, false) {
		fmt.Println(n)
	}
	fmt.Println("已從 IBus 移除靈犀注音；GTK／Qt 輸入設定在重新登入後還原。")
	return nil
}
