package vocabapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// 設定與 token 都放在個人資料目錄（user.json 旁邊），只有使用者本人能讀（0600；Windows 的
// %LOCALAPPDATA% 本來就只有本人有權限）。token 不放網址、不寫日誌。
const (
	ConfigFile  = "api.json"
	TokenFile   = "api-token.json"
	DefaultPort = 47100
)

// Config 是「讓 AI 幫你加詞」的開關與埠號，由 0c-update api enable／disable 修改、引擎啟動時讀取。
type Config struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port,omitempty"` // 0 表示 DefaultPort
}

// Token 是本機 API 的存取權杖。
type Token struct {
	Value   string `json:"token"`
	Created int64  `json:"created"` // Unix 秒
}

func (c Config) Addr() string {
	port := c.Port
	if port <= 0 || port > 65535 {
		port = DefaultPort
	}
	return "127.0.0.1:" + strconv.Itoa(port)
}

// LoadConfig 讀設定；檔案不存在時是預設值（關閉）。
func LoadConfig(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// SaveConfig 寫設定。
func SaveConfig(dir string, c Config) error { return writePrivate(filepath.Join(dir, ConfigFile), c) }

// LoadToken 讀 token；不存在時回傳 fs.ErrNotExist。
func LoadToken(dir string) (Token, error) {
	var t Token
	b, err := os.ReadFile(filepath.Join(dir, TokenFile))
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(b, &t); err != nil || t.Value == "" {
		return Token{}, errors.New("token 檔損毀，請重新產生")
	}
	return t, nil
}

// EnsureToken 讀 token，沒有時產生一個。
func EnsureToken(dir string) (Token, error) {
	t, err := LoadToken(dir)
	if err == nil {
		return t, nil
	}
	return RegenToken(dir)
}

// RegenToken 產生新的 token，舊的下一個請求起就無效（引擎每次驗證都看檔案的修改時間）。
func RegenToken(dir string) (Token, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Token{}, err
	}
	t := Token{Value: "0c_" + hex.EncodeToString(raw[:]), Created: time.Now().Unix()}
	return t, writePrivate(filepath.Join(dir, TokenFile), t)
}

// writePrivate 先寫暫存檔再改名，權限 0600。
func writePrivate(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".api-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
