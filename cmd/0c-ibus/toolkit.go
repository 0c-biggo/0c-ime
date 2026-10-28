//go:build linux

package main

// GTK／Qt 改用 IBus 自己的輸入模組。GNOME Wayland 原生路徑在焦點移走時會丟掉組字（mutter 先送 leave
// 才提交），程式改走 IBus 的 GTK 模組或 Qt 外掛，就由程式自己在失焦時提交。每個工具包一個
// environment.d 檔案，有三種狀態：沒有檔案（從未設定）、有設定（是我們設的）、只有停用記號（使用者以
// -gtk-module=false／-qt-module=false 停用，之後重跑安裝或更新都不加回來）。使用者自己設了別的值時
// 不寫，也不清他的值。

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type toolkit struct {
	label, flag, file string
	name, value       string
	comment           string
	// inherited 是不算使用者自訂的值：沒設定、我們的值，以及桌面自己的預設。
	inherited map[string][]string
}

var toolkits = []toolkit{
	{
		label: "GTK", flag: "gtk-module", file: gtkModuleEnv,
		name: "GTK_IM_MODULE", value: "ibus",
		comment:   "GNOME Wayland 的原生輸入路徑在焦點移走時會丟掉組字；GTK 程式改用 IBus 的 GTK 模組，由程式自己提交。",
		inherited: map[string][]string{"GTK_IM_MODULE": {"", "ibus"}},
	},
	{
		// Qt 6.8 起依 QT_IM_MODULES 的順序選輸入方式；gnome-session 在它空著時補 wayland;ibus（先走原生路徑）。
		// 把 ibus 排第一，載不到 IBus 外掛的程式（自帶 Qt 的 AppImage 等）仍可退回 wayland。Qt 5 只讀 QT_IM_MODULE。
		label: "Qt", flag: "qt-module", file: qtModuleEnv,
		name: "QT_IM_MODULES", value: "ibus;wayland",
		comment: "Qt 6.8 起依 QT_IM_MODULES 的順序選輸入方式；GNOME 預設先走 Wayland 原生路徑，焦點移走時會丟掉組字。",
		inherited: map[string][]string{
			"QT_IM_MODULES": {"", "ibus;wayland", "wayland;ibus", "ibus"},
			"QT_IM_MODULE":  {"", "ibus"}, // 使用者指定 fcitx 等其他輸入法時，QT_IM_MODULES 會讓 Qt 6.8 忽略它
		},
	},
}

// etcDir 是系統設定目錄；測試換成暫存目錄。
var etcDir = "/etc"

const disabledMark = "# 已停用：0c-ibus register -%s=false。重跑 register -%s=true 才恢復。\n"

func (t toolkit) setting() string {
	return "# " + t.comment + "\n" + t.name + "=" + t.value + "\n"
}

// state 回報這個工具包的設定檔：有沒有檔案、是不是有設定（不是停用記號）。
func (t toolkit) state(home string) (exists, enabled bool) {
	b, err := os.ReadFile(filepath.Join(home, t.file))
	if err != nil {
		return false, false
	}
	return true, strings.Contains(string(b), "\n"+t.name+"=") || strings.HasPrefix(string(b), t.name+"=")
}

// envAssignments 讀 environment.d 風格的檔案，回傳其中 NAME=value 的設定。
func envAssignments(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s.Text()), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// userSetting 找使用者或系統自己指定的值（不是我們寫的、也不是桌面預設），回傳 "NAME=value"；
// withEnv 時也看目前的環境，但我們的設定生效中時，自己的變數值就是我們寫的，不看。
func (t toolkit) userSetting(home string, withEnv bool) string {
	files := []string{filepath.Join(etcDir, "environment")}
	for _, dir := range []string{filepath.Join(home, ".config/environment.d"), filepath.Join(etcDir, "environment.d")} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
		files = append(files, matches...)
	}
	_, enabled := t.state(home)
	for name, ok := range t.inherited {
		for _, f := range files {
			if f == filepath.Join(home, t.file) {
				continue
			}
			if v, set := envAssignments(f)[name]; set && !slices.Contains(ok, v) {
				return name + "=" + v
			}
		}
		if withEnv && !(enabled && name == t.name) {
			if v := os.Getenv(name); !slices.Contains(ok, v) {
				return name + "=" + v
			}
		}
	}
	return ""
}

// syncToolkits 依 want 設定各工具包；want 沒列的工具包維持原狀，只補上從未設定過的。
// 回傳要重新登入才生效的工具包名稱。
func syncToolkits(home string, want map[string]bool) ([]string, error) {
	var relogin []string
	for _, t := range toolkits {
		path := filepath.Join(home, t.file)
		exists, enabled := t.state(home)
		on, asked := want[t.flag]
		if !asked {
			on = enabled || !exists // 停用記號：維持停用
		}
		if !on {
			if !exists || enabled {
				if err := replaceFile(path, fmt.Sprintf(disabledMark, t.flag, t.flag)); err != nil {
					return relogin, err
				}
			}
			if enabled {
				exec.Command("systemctl", "--user", "unset-environment", t.name).Run()
				relogin = append(relogin, t.label)
			}
			continue
		}
		if own := t.userSetting(home, true); own != "" {
			// 我們的檔案排在後面會蓋掉使用者的設定；移掉它，但不清環境變數（值可能是使用者的）。
			if exists {
				if err := os.Remove(path); err != nil {
					return relogin, err
				}
				relogin = append(relogin, t.label)
			}
			fmt.Printf("保留你自己的 %s，不改 %s 程式的輸入設定。\n", own, t.label)
			continue
		}
		changed, err := writeFile(path, t.setting())
		if err != nil {
			return relogin, err
		}
		if changed {
			// 使用者管理員已在執行（例如 linger）時不會重讀 environment.d；GNOME 下次登入取的是管理員的環境。
			exec.Command("systemctl", "--user", "set-environment", t.name+"="+t.value).Run()
			relogin = append(relogin, t.label)
		}
	}
	return relogin, nil
}

// removeToolkits 撤銷所有工具包設定。只有檔案裡是我們的設定、而且沒有使用者自己的值時才清環境變數；
// 從 environment.d 讀進 systemd 使用者管理員的值清不掉，刪檔後重新登入才還原。
func removeToolkits(home string) error {
	for _, t := range toolkits {
		_, enabled := t.state(home)
		if err := os.Remove(filepath.Join(home, t.file)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if enabled && t.userSetting(home, false) == "" {
			exec.Command("systemctl", "--user", "unset-environment", t.name).Run()
		}
	}
	return nil
}

// syncRegisteredToolkits 是 toolkits 子命令：只在已登錄時補上新版本加入的工具包設定與 X11 工作階段設定。
func syncRegisteredToolkits() error {
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, componentFile)); err != nil {
		return nil
	}
	relogin, err := syncToolkits(home, nil)
	if err != nil {
		return err
	}
	reloginNote(relogin)
	// 0.2.6 以前在 Cinnamon 等 X11 桌面登錄時沒寫 ~/.xsessionrc，也沒加輸入來源，IBus 一直列不出靈犀注音
	// （#237）：第一次補上時一併刪掉不含使用者目錄的登錄快取並加入輸入來源，重新登入後生效。不重啟 IBus。
	changed, err := syncSessionRC(home, true)
	if err != nil || !changed {
		return err
	}
	os.Remove(filepath.Join(home, registryCache))
	addSource()
	fmt.Println("已把靈犀注音的 IBus 元件路徑加入 ~/.xsessionrc，重新登入後生效。")
	return nil
}

func reloginNote(labels []string) {
	if len(labels) > 0 {
		fmt.Println(strings.Join(labels, "／") + " 程式的輸入設定在重新登入後生效。")
	}
}
