//go:build windows

package settingsapp

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// StartMenu 是目前使用者的開始功能表「程式」資料夾。
func StartMenu() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
}

// EnsureStartMenuShortcut 建立（或更新）開始功能表的「靈犀注音設定」。
func EnsureStartMenuShortcut(base, stableUpdater string) (bool, error) {
	conhost := filepath.Join(os.Getenv("SystemRoot"), `System32\conhost.exe`)
	spec, err := NewShortcutSpec(StartMenu(), base, conhost, stableUpdater)
	if err != nil {
		return false, err
	}
	return EnsureShortcut(base, spec, createShortcut)
}

func createShortcut(spec ShortcutSpec) error {
	script := utf16.Encode([]rune(ShortcutScript))
	raw := make([]byte, len(script)*2)
	for i, v := range script {
		raw[2*i], raw[2*i+1] = byte(v), byte(v>>8)
	}
	ps := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	cmd.Env = append(os.Environ(), "ZC_LNK_PATH="+spec.Path, "ZC_LNK_TARGET="+spec.Target, "ZC_LNK_ARGS="+spec.Args,
		"ZC_LNK_DIR="+spec.Dir, "ZC_LNK_ICON="+spec.Icon)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("建立捷徑失敗：%s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
