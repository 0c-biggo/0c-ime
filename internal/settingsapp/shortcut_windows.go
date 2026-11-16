//go:build windows

package settingsapp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/winshell"
)

// StartMenu 是目前使用者的開始功能表「程式」資料夾。
func StartMenu() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
}

// StartMenuSpec 是開始功能表「靈犀注音設定（0c）」的內容，指向設定程式的穩定副本 settings。
func StartMenuSpec(base, settings string) (ShortcutSpec, error) {
	return NewShortcutSpec(StartMenu(), base, settings)
}

// EnsureStartMenuShortcut 建立（或更新）開始功能表的「靈犀注音設定（0c）」；restore 見 EnsureShortcut。
func EnsureStartMenuShortcut(base string, spec ShortcutSpec, restore bool) (bool, error) {
	return EnsureShortcut(base, spec, restore, createShortcut)
}

// createShortcut 在行程內以 IShellLinkW 寫出 .lnk（#289：不再開 powershell.exe -EncodedCommand）。新建的物件不讀入
// 既有的同名捷徑，參數一定明確寫入（沒有參數時是空字串），舊樣式的 --headless 參數不會留下來。
func createShortcut(spec ShortcutSpec) error {
	err := winshell.CreateShortcut(winshell.Shortcut{Path: spec.Path, Target: spec.Target, Args: spec.Args, Dir: spec.Dir,
		Icon: spec.Icon, Description: ShortcutDescription})
	if err != nil {
		return fmt.Errorf("建立捷徑失敗：%w", err)
	}
	return nil
}
