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

// ShortcutDescription 是捷徑的說明（滑鼠停留時的提示）。
const ShortcutDescription = "設定靈犀注音輸入法"

// NewShortcutSpec 組出捷徑：直接指向個人資料目錄 bin 裡設定程式的穩定副本（GUI 程式，不會出現主控台視窗），
// 不帶參數。路徑不隨版本變動，所以捷徑只在第一次安裝、從舊樣式換新時寫一次。
//
// #284：先前的捷徑是 conhost.exe --headless "<0c-update.exe>" open-settings，「捷徑經 conhost 隱藏視窗啟動另一個
// 程式」正是惡意 LNK 的常見手法，Symantec Endpoint Protection 以 Scr.Malcode!gen 隔離。其他輸入法（新酷音
// windows-chewing-tsf 的 ChewingPreferences.exe、PIME 的 pythonw.exe）也都直接指向不開主控台的程式。
func NewShortcutSpec(startMenu, base, settings string) (ShortcutSpec, error) {
	for _, p := range []string{startMenu, base, settings} {
		if !filepath.IsAbs(p) || strings.ContainsRune(p, '"') {
			return ShortcutSpec{}, fmt.Errorf("需要不含引號的絕對路徑：%q", p)
		}
	}
	return ShortcutSpec{
		Schema: 1,
		Path:   filepath.Join(startMenu, ShortcutFolder, ShortcutName),
		Target: settings,
		Dir:    filepath.Dir(settings),
		Icon:   filepath.Join(base, filepath.FromSlash(ShortcutIcon)),
	}, nil
}

// Recorded：標記檔記錄的就是這個 spec，也就是這個捷徑我們已經建過。
func Recorded(base string, spec ShortcutSpec) bool {
	old, err := readMarker(filepath.Join(base, ShortcutMarker))
	return err == nil && old == spec
}

// Withdrawn：這個捷徑我們建過，之後被使用者或防毒軟體移除了。這時除了重新安裝（restore）都不再建立，
// 否則每次自動更新排程都重建一次，防毒軟體就每 15 分鐘跳一次通知（#284）。
func Withdrawn(base string, spec ShortcutSpec) bool {
	return Recorded(base, spec) && !exists(spec.Path)
}

// EnsureShortcut 讓捷徑與 spec 一致；內容沒變、捷徑與圖示都還在時什麼都不做（回傳 false）。
// 內容沒變而捷徑不見了（Withdrawn）只在 restore（完整安裝）時補回。create 實際建立 .lnk，由平台提供，測試以假的取代。
func EnsureShortcut(base string, spec ShortcutSpec, restore bool, create func(ShortcutSpec) error) (bool, error) {
	marker := filepath.Join(base, ShortcutMarker)
	old, oldErr := readMarker(marker)
	if oldErr == nil && old == spec && !exists(spec.Path) && !restore {
		return false, nil
	}
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

// WithdrawShortcut 只移除標記檔記錄的捷徑、保留標記檔：捷徑指向的穩定副本不見了（被防毒軟體隔離）時，
// 不留下點了會出錯的捷徑，之後也照 Withdrawn 不再自動重建。
func WithdrawShortcut(base string) error {
	spec, err := readMarker(filepath.Join(base, ShortcutMarker))
	if err != nil {
		return nil
	}
	if err := os.Remove(spec.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
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
