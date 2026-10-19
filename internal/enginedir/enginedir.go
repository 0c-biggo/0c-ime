// Package enginedir 管理「目前使用的引擎版本」：個人資料目錄下的 engine.json 指向
// versions/ 內的一個版本目錄（引擎、配對字典與神經模型）。相容核心更新只改這個指標，
// 外殼（TSF DLL、Fcitx addon）不變；檔案不存在或無效時，外殼沿用原本的引擎位置。
package enginedir

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const FileName = "engine.json"

type Current struct {
	Schema    int    `json:"schema"`
	Directory string `json:"directory"` // 相對個人資料目錄，一律以 versions/ 開頭、使用正斜線
	Version   string `json:"version"`
	// Previous 是切換前的指標（核心更新寫入）：與新指標同一次原子寫入，切換中途被中止時仍能正確回復。
	// 沒有時表示切換前沒有指標（回復即刪除指標）。外殼只讀 directory，忽略此欄位。
	Previous *Current `json:"previous,omitempty"`
}

func safe(dir string) bool {
	if !strings.HasPrefix(dir, "versions/") || strings.Contains(dir, "\\") {
		return false
	}
	for _, part := range strings.Split(dir, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return strings.Count(dir, "/") == 1
}

// Read 讀取目前的引擎指標；不存在時回傳 nil, nil。
func Read(base string) (*Current, error) {
	b, err := os.ReadFile(filepath.Join(base, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Current
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Schema != 1 || !safe(c.Directory) {
		return nil, fmt.Errorf("invalid engine pointer")
	}
	return &c, nil
}

// Write 以暫存檔改名原子更新指標。
func Write(base string, c Current) error {
	c.Schema = 1
	if !safe(c.Directory) {
		return fmt.Errorf("invalid engine directory %q", c.Directory)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(base, ".engine-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(base, FileName))
}

// Remove 刪除指標，外殼回到安裝時的引擎（完整安裝使用）。
func Remove(base string) error {
	err := os.Remove(filepath.Join(base, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Executable 是版本目錄中的引擎檔名。
func Executable() string {
	if runtime.GOOS == "windows" {
		return "0c-server.exe"
	}
	return "0c-server"
}

// Resolve 回傳指標所指的引擎與配對字典目錄；指標不存在、無效或檔案不齊全時 ok 為 false。
func Resolve(base string) (exe, data string, ok bool) {
	c, err := Read(base)
	if err != nil || c == nil {
		return "", "", false
	}
	dir := filepath.Join(base, filepath.FromSlash(c.Directory))
	exe, data = filepath.Join(dir, Executable()), filepath.Join(dir, "data")
	st, err := os.Lstat(exe)
	if err != nil || !st.Mode().IsRegular() {
		return "", "", false
	}
	if st, err = os.Lstat(filepath.Join(data, "manifest.json")); err != nil || !st.Mode().IsRegular() {
		return "", "", false
	}
	return exe, data, true
}

// FilesName 記錄核心版本目錄內最上層檔案（引擎、更新器、設定程式）複製當下核對過的 SHA-256。
// 啟動設定程式前據此再核對一次，避免啟動不完整或被替換的檔案。
const FilesName = "core-files.json"

type files struct {
	Schema int               `json:"schema"`
	Files  map[string]string `json:"files"`
}

// WriteFiles 把檔名對應的雜湊寫進版本目錄（dir 是版本目錄的實際路徑）。
func WriteFiles(dir string, hashes map[string]string) error {
	b, err := json.MarshalIndent(files{Schema: 1, Files: hashes}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FilesName), append(b, '\n'), 0o600)
}

// ReadFiles 讀取版本目錄記錄的雜湊；沒有記錄時（舊版核心更新準備的目錄）回傳 nil, nil。
func ReadFiles(dir string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, FilesName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f files
	if err = json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Schema != 1 {
		return nil, fmt.Errorf("unsupported %s", FilesName)
	}
	return f.Files, nil
}

// SettingsExecutable 是版本目錄中設定程式的檔名。
func SettingsExecutable() string {
	if runtime.GOOS == "windows" {
		return "0c-settings.exe"
	}
	return "0c-settings"
}

// Dir 回傳目前引擎指標所指的版本目錄的實際路徑；沒有有效指標時 ok 為 false。
func Dir(base string) (dir string, ok bool) {
	c, err := Read(base)
	if err != nil || c == nil {
		return "", false
	}
	return filepath.Join(base, filepath.FromSlash(c.Directory)), true
}
