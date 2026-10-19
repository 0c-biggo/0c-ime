package settingsapp

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// DesktopFile 是應用程式選單的項目（使用者層級，不需要管理員權限）。檔名必須是設定程式向桌面宣告的
	// app id：GTK 在 Wayland 上宣告的是程式名稱 0c-settings（在 GNOME 50 實測 xdg_toplevel.set_app_id），
	// 不是 Tauri 的 identifier；GNOME 依它把視窗和這個項目對起來，才有正確的圖示與分組。
	DesktopFile   = "0c-settings.desktop"
	desktopMarker = "X-0c-Managed=true" // 只更新、移除帶這一行的檔案；使用者自己建立的同名檔案不碰
	iconName      = "0c-settings"
	iconPath      = "icons/hicolor/128x128/apps/" + iconName + ".png"
)

// icon 與 platforms/settings/icons/128x128.png 相同（測試核對）；寫到使用者的圖示目錄，
// 應用程式選單、Dock 與切換視窗才會顯示品牌圖示。
//
//go:embed icon.png
var icon []byte

// DataHome 是使用者的 XDG 資料目錄。
func DataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// desktopContent 產生項目內容。Exec 不會展開 ~ 或環境變數，所以寫絕對路徑；路徑含需要跳脫的字元時
// 寧可不建立項目（回傳錯誤），也不寫出可能被解讀成其他命令的 Exec。
func desktopContent(updater string) (string, error) {
	if !filepath.IsAbs(updater) {
		return "", fmt.Errorf("需要絕對路徑：%q", updater)
	}
	for _, r := range updater {
		if unicode.IsControl(r) || strings.ContainsRune("\"`$\\%", r) {
			return "", fmt.Errorf("路徑含無法安全寫入 Exec 的字元：%q", updater)
		}
	}
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Version=1.0
Name=靈犀注音設定
Comment=設定靈犀注音輸入法
Exec="%s" open-settings
Icon=%s
Terminal=false
Categories=Settings;
StartupNotify=false
StartupWMClass=0c-settings
%s
`, updater, iconName, desktopMarker), nil
}

// replaceFile 內容不同才寫入（暫存檔改名，不留下寫到一半的檔案）；回傳是否有寫入。
func replaceFile(path string, data []byte, perm os.FileMode) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".0c-settings-")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err = tmp.Chmod(perm); err != nil {
		tmp.Close()
		return false, err
	}
	if err = tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// EnsureDesktopEntry 在應用程式選單建立（或更新）「靈犀注音設定」項目與它的圖示，項目指向穩定的 0c-update。
// 內容沒變時不寫檔；同名但不是本程式建立的項目不覆寫。回傳是否有寫入。
func EnsureDesktopEntry(dataHome, updater string) (bool, error) {
	content, err := desktopContent(updater)
	if err != nil {
		return false, err
	}
	path := filepath.Join(dataHome, "applications", DesktopFile)
	if current, err := os.ReadFile(path); err == nil && !strings.Contains(string(current), desktopMarker) {
		return false, fmt.Errorf("%s 不是本程式建立的，不覆寫", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	wroteIcon, err := replaceFile(filepath.Join(dataHome, iconPath), icon, 0o644)
	if err != nil {
		return false, err
	}
	wroteEntry, err := replaceFile(path, []byte(content), 0o644)
	return wroteIcon || wroteEntry, err
}

// RemoveDesktopEntry 移除本程式建立的項目；不存在或不是本程式建立的就不動。
func RemoveDesktopEntry(dataHome string) error {
	path := filepath.Join(dataHome, "applications", DesktopFile)
	current, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !strings.Contains(string(current), desktopMarker)) {
		return nil
	}
	if err != nil {
		return err
	}
	if current, err := os.ReadFile(filepath.Join(dataHome, iconPath)); err == nil && bytes.Equal(current, icon) {
		_ = os.Remove(filepath.Join(dataHome, iconPath)) // 只移除我們寫的圖示
	}
	return os.Remove(path)
}
