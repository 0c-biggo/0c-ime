//go:build linux

package main

// 桌面相關的登錄步驟：輸入來源清單、X11 工作階段的元件路徑、重新啟動 ibus-daemon。
//
// GNOME 由 systemd 使用者服務啟動 ibus-daemon，讀得到 environment.d。其他 X11 桌面（Cinnamon、MATE、
// Xfce）的 ibus-daemon 由桌面或 im-config 在 Xsession 裡啟動，只有 Xsession 的環境，environment.d
// 到不了；Debian 系（Ubuntu、Mint）的 Xsession 會讀 ~/.xsessionrc，元件路徑寫在那裡（#237）。

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	gnomeSources    = "org.gnome.desktop.input-sources"
	cinnamonSources = "org.cinnamon.desktop.input-sources"

	sessionRC    = ".xsessionrc"
	sessionBegin = "# >>> 0c-ibus：靈犀注音的 IBus 元件路徑（0c-ibus unregister 會移除這一段）"
	sessionEnd   = "# <<< 0c-ibus"
	// sessionBody 只把使用者元件目錄接在原本的路徑後面；已經有了就不動。用 case 寫，結束狀態一定是 0：
	// Xsession 本身以 set -e 執行，現在的 Debian Xsession 與 lightdm-session 讀 Xsession.d 時雖然不中斷，
	// 也不依賴這一點（[ -d … ] && export 在條件不成立時結束狀態不是 0）。
	sessionBody = `case ":${IBUS_COMPONENT_PATH:-/usr/share/ibus/component}:" in
*":$HOME/.local/share/ibus/component:"*) ;;
*) IBUS_COMPONENT_PATH="${IBUS_COMPONENT_PATH:-/usr/share/ibus/component}:$HOME/.local/share/ibus/component"
   export IBUS_COMPONENT_PATH ;;
esac
`
)

// procDir 是 /proc；測試換成暫存目錄。
var procDir = "/proc"

func desktopIs(name string) bool {
	return strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), name)
}

func gnome() bool { return desktopIs("GNOME") }

// sourceSchema 是目前桌面的輸入來源清單（格式同 GNOME：a(ss)）；沒有對應清單的桌面（MATE、Xfce 等用
// IBus 自己的面板與 preload-engines）回傳空字串。
func sourceSchema() string {
	switch {
	case gnome():
		return gnomeSources
	case desktopIs("CINNAMON"):
		return cinnamonSources
	}
	return ""
}

func gsettingsSources(schema string) (string, error) {
	out, err := exec.Command("gsettings", "get", schema, "sources").Output()
	return strings.TrimSpace(string(out)), err
}

func setSources(schema string, edit func(string) string) error {
	cur, err := gsettingsSources(schema)
	if err != nil {
		return err
	}
	if next := edit(cur); next != cur {
		return exec.Command("gsettings", "set", schema, "sources", next).Run()
	}
	return nil
}

// addSource 把靈犀注音加到目前桌面的輸入來源清單；沒有清單的桌面說明怎麼在 IBus 偏好設定加入。
func addSource() {
	switch schema := sourceSchema(); {
	case schema == "":
		fmt.Println("請在「IBus 偏好設定」（ibus-setup）的「輸入法」加入「中文 → 靈犀注音」，之後以 IBus 的切換鍵（預設 Super+空白鍵）切換。")
	case setSources(schema, func(cur string) string { return withSource(cur, systemLayout()) }) != nil:
		if schema == cinnamonSources {
			fmt.Println("無法加入輸入來源，請在「系統設定 → 鍵盤 → 輸入法」加入「靈犀注音」。")
		} else {
			fmt.Println("無法加入 GNOME 輸入來源，請在「設定 → 鍵盤 → 輸入來源」加入「中文（靈犀注音）」。")
		}
	}
}

// removeSource 從所有認得的輸入來源清單移除靈犀注音（不只目前的桌面：同一個帳號可能也登入別的桌面）。
func removeSource() {
	for _, schema := range []string{gnomeSources, cinnamonSources} {
		setSources(schema, withoutSource) // 沒有這個清單或沒有桌面工作階段時失敗：不影響移除
	}
}

// needsSessionRC：這個桌面的 ibus-daemon 讀不到 environment.d，而且 Xsession 會讀 ~/.xsessionrc。
func needsSessionRC() bool {
	_, err := os.Stat(filepath.Join(etcDir, "X11/Xsession.d/40x11-common_xsessionrc"))
	return !gnome() && err == nil
}

// syncSessionRC 在 ~/.xsessionrc 加上（on）或移除我們那一段，其餘內容不動；回報是否改了檔案。
// GNOME 與沒有 ~/.xsessionrc 機制的系統不新增，已經有的也留著（內容只是重複設定同一個路徑）。
func syncSessionRC(home string, on bool) (bool, error) {
	path := filepath.Join(home, sessionRC)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // 使用者把設定檔連到別處時改那個檔案，不把連結換成一般檔案
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	rest, had := withoutSessionBlock(string(old))
	next := rest
	switch {
	case on && !had && !needsSessionRC():
		return false, nil
	case on:
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += sessionBegin + "\n" + sessionBody + sessionEnd + "\n"
	case !had:
		return false, nil
	}
	if next == string(old) {
		return false, nil
	}
	if strings.TrimSpace(next) == "" {
		return true, os.Remove(path)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".0c-new"
	if err := os.WriteFile(tmp, []byte(next), mode); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// withoutSessionBlock 去掉我們那一段（含開頭、結尾標記），回報原本有沒有。
func withoutSessionBlock(s string) (string, bool) {
	start := strings.Index(s, sessionBegin)
	if start < 0 {
		return s, false
	}
	end := strings.Index(s[start:], sessionEnd)
	if end < 0 {
		return s, false // 結尾標記被使用者刪掉：不猜範圍，整個保留
	}
	end += start + len(sessionEnd)
	if end < len(s) && s[end] == '\n' {
		end++
	}
	return s[:start] + s[end:], true
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
	out, err := exec.Command("pgrep", "-n", "-u", fmt.Sprint(os.Getuid()), "-x", "ibus-daemon").Output()
	if err != nil {
		return notes // 沒有在執行：下次登入（或桌面啟動它）時就讀新的路徑
	}
	if err := replaceDaemon(strings.TrimSpace(string(out)), home, registered); err != nil {
		notes = append(notes, "無法重新啟動 IBus（"+err.Error()+"）；重新登入後生效。")
	}
	return notes
}

// replaceDaemon 以執行中 ibus-daemon 原本的參數與環境（只改 IBUS_COMPONENT_PATH）啟動新的 daemon 取代它。
// 沿用原本的參數才不會改變桌面的安排，例如 Cinnamon 自己當面板（--panel disable）、X11 的 --xim。
// --cache=refresh 不採用登錄快取：快取不記得當初的搜尋路徑，新加的目錄不會讓它失效。
func replaceDaemon(pid, home string, registered bool) error {
	cmdline, err := os.ReadFile(filepath.Join(procDir, pid, "cmdline"))
	if err != nil {
		return err
	}
	argv := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	bin := argv[0]
	if !filepath.IsAbs(bin) {
		if bin, err = exec.LookPath("ibus-daemon"); err != nil {
			return err
		}
	}
	env := os.Environ()
	if b, err := os.ReadFile(filepath.Join(procDir, pid, "environ")); err == nil {
		env = strings.Split(string(bytes.TrimRight(b, "\x00")), "\x00")
	}
	kept := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "IBUS_COMPONENT_PATH=") {
			kept = append(kept, kv)
		}
	}
	if registered {
		kept = append(kept, "IBUS_COMPONENT_PATH="+componentPath(home))
	}
	// --daemonize 讓啟動的行程立刻結束，daemon 不留在安裝程式底下。先拿掉原本的同一組旗標，
	// 重複登錄、移除時參數才不會越疊越長（合併的短旗標如 -rdx 留著，重複無妨）。
	var args []string
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "-r" || a == "--replace" || a == "-d" || a == "--daemonize" || strings.HasPrefix(a, "--cache="):
		case a == "-t" || a == "--cache":
			i++ // 連同下一個參數（快取模式）
		default:
			args = append(args, a)
		}
	}
	cmd := exec.Command(bin, append(args, "--replace", "--daemonize", "--cache=refresh")...)
	cmd.Env = kept
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("ibus-daemon 沒有回應")
	}
}
