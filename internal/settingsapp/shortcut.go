package settingsapp

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// 開始功能表項目（Windows）：與安裝精靈的「使用說明」「解除安裝」放在同一個資料夾，使用者層級，不需要管理員權限。
	ShortcutFolder = "靈犀注音"
	ShortcutName   = "靈犀注音設定（0c）.lnk"
	// ShortcutIcon 與 ShortcutMarker 放在個人資料目錄：圖示不跟著版本目錄走（版本目錄會被清理），
	// 標記檔記錄這個項目是我們建立的內容，之後只更新、移除自己建立的那一個。
	ShortcutIcon   = "bin/0c-settings.ico"
	ShortcutMarker = "settings-entry.json"
)

// ico 是品牌圖示的 ico 檔（16–256 px），與 platforms/settings/icons/icon.ico 相同（測試核對）。
//
//go:embed icon.ico
var ico []byte

// ShortcutSpec 是捷徑的內容，也是標記檔的內容：內容沒變就不必重建。
type ShortcutSpec struct {
	Schema int    `json:"schema"`
	Path   string `json:"path"`
	Target string `json:"target"`
	Args   string `json:"args"`
	Dir    string `json:"dir"`
	Icon   string `json:"icon"`
}

// ShortcutScript 以環境變數取得內容，所以路徑裡的引號、空白或中文都不必跳脫。
const ShortcutScript = `$ErrorActionPreference = 'Stop'
$shell = New-Object -ComObject WScript.Shell
$link = $shell.CreateShortcut($env:ZC_LNK_PATH)
$link.TargetPath = $env:ZC_LNK_TARGET
$link.Arguments = $env:ZC_LNK_ARGS
$link.WorkingDirectory = $env:ZC_LNK_DIR
$link.IconLocation = $env:ZC_LNK_ICON + ',0'
$link.Description = '設定靈犀注音輸入法'
$link.Save()
`

// NewShortcutSpec 組出捷徑：以 conhost --headless 執行穩定的 0c-update（與自動更新排程相同的做法），
// 命令列程式不會閃出主控台視窗。路徑含引號時無法安全地放進引數，直接拒絕。
func NewShortcutSpec(startMenu, base, conhost, stableUpdater string) (ShortcutSpec, error) {
	for _, p := range []string{startMenu, base, conhost, stableUpdater} {
		if !filepath.IsAbs(p) || strings.ContainsRune(p, '"') {
			return ShortcutSpec{}, fmt.Errorf("需要不含引號的絕對路徑：%q", p)
		}
	}
	return ShortcutSpec{
		Schema: 1,
		Path:   filepath.Join(startMenu, ShortcutFolder, ShortcutName),
		Target: conhost,
		Args:   fmt.Sprintf(`--headless "%s" open-settings`, stableUpdater),
		Dir:    base,
		Icon:   filepath.Join(base, filepath.FromSlash(ShortcutIcon)),
	}, nil
}

// EnsureShortcut 讓捷徑與 spec 一致；內容沒變、捷徑與圖示都還在時什麼都不做（回傳 false）。
// create 實際建立 .lnk，由平台提供，測試以假的取代。
func EnsureShortcut(base string, spec ShortcutSpec, create func(ShortcutSpec) error) (bool, error) {
	marker := filepath.Join(base, ShortcutMarker)
	old, oldErr := readMarker(marker)
	if oldErr == nil && old == spec && exists(spec.Path) && exists(spec.Icon) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(spec.Icon), 0o700); err != nil {
		return false, err
	}
	if err := writeIfChanged(spec.Icon, ico); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o755); err != nil {
		return false, err
	}
	if err := create(spec); err != nil {
		return false, err
	}
	// 新名稱包含 0c，供開始功能表搜尋；新捷徑成功後才移除標記記錄的舊名稱。
	if oldErr == nil && old.Path != spec.Path {
		if err := os.Remove(old.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(marker, append(b, '\n'), 0o600)
}

// RemoveShortcut 移除先前建立的捷徑（依標記檔）；沒有標記檔就什麼都不動，所以不會刪到使用者自己建立的檔案。
func RemoveShortcut(base string) error {
	marker := filepath.Join(base, ShortcutMarker)
	spec, err := readMarker(marker)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		if rmErr := os.Remove(spec.Path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return rmErr
		}
		_ = os.Remove(filepath.Dir(spec.Path)) // 資料夾裡還有別的項目時刪不掉，保留
	}
	return os.Remove(marker)
}

func readMarker(path string) (ShortcutSpec, error) {
	var spec ShortcutSpec
	b, err := os.ReadFile(path)
	if err != nil {
		return spec, err
	}
	if err = json.Unmarshal(b, &spec); err != nil {
		return ShortcutSpec{}, err
	}
	if spec.Schema != 1 {
		return ShortcutSpec{}, errors.New("不認得的標記檔")
	}
	return spec, nil
}

func exists(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular()
}

func writeIfChanged(path string, content []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	return os.WriteFile(path, content, 0o644)
}
