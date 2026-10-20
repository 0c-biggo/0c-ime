// Package englishpack owns English dictionary files; core/english only handles
// immutable vocabulary, indexing and byte encoding.
package englishpack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/0c-biggo/0c-ime/core/english"
)

type File struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Source struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
}
type Manifest struct {
	Format  int               `json:"format"`
	Words   int               `json:"words"`
	Ranking string            `json:"ranking"`
	Sources []Source          `json:"sources"`
	Inputs  map[string]string `json:"inputs"`
	Files   map[string]File   `json:"files"`
}

func Sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func read(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("不是一般檔案或檔案過大：%s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("檔案過大：%s", path)
	}
	return b, err
}

func Load(dir string) (*english.Dictionary, *Manifest, error) { return LoadWithTransform(dir, nil) }

// transform is the product's existing dictionary unsealer, applied before
// checking plaintext hashes. The English manifest is independent of Chinese.
func LoadWithTransform(dir string, transform func([]byte) ([]byte, error)) (*english.Dictionary, *Manifest, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("英文資料包必須是一般目錄")
	}
	b, err := read(filepath.Join(dir, "manifest.json"), 64<<10)
	if err != nil {
		return nil, nil, err
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, nil, err
	}
	if err = m.validate(); err != nil {
		return nil, nil, err
	}
	files := map[string][]byte{}
	for _, name := range []string{"words.bin", "NOTICE.txt"} {
		limit := int64(64 << 10)
		if name == "words.bin" {
			limit = english.MaxPackBytes + 64
		}
		data, err := read(filepath.Join(dir, name), limit)
		if err != nil {
			return nil, nil, err
		}
		if name == "words.bin" && transform != nil {
			data, err = transform(data)
			if err != nil {
				return nil, nil, err
			}
		}
		if info := m.Files[name]; info.Size != int64(len(data)) || info.SHA256 != Sum(data) {
			return nil, nil, fmt.Errorf("英文 %s 與 manifest 不符", name)
		}
		files[name] = data
	}
	if strings.TrimSpace(string(files["NOTICE.txt"])) == "" {
		return nil, nil, fmt.Errorf("英文授權聲明不可空白")
	}
	d, err := english.Decode(files["words.bin"])
	if err != nil {
		return nil, nil, err
	}
	if d.Len() != m.Words {
		return nil, nil, fmt.Errorf("英文 manifest 詞數不符")
	}
	return d, &m, nil
}

func (m Manifest) validate() error {
	if m.Format != 1 || m.Words < 1 || m.Words > english.MaxPackWords || m.Ranking == "" || len(m.Sources) == 0 || len(m.Files) != 2 {
		return fmt.Errorf("英文 manifest 格式不合法")
	}
	for _, name := range []string{"words.bin", "NOTICE.txt"} {
		f, ok := m.Files[name]
		if !ok || f.Size < 1 || !hashOK(f.SHA256) {
			return fmt.Errorf("英文 manifest 缺少 %s 雜湊或大小", name)
		}
	}
	for _, s := range m.Sources {
		if s.Name == "" || s.Version == "" || s.URL == "" || s.License == "" || s.Attribution == "" || !hashOK(s.SHA256) {
			return fmt.Errorf("英文資料包缺少來源與授權")
		}
	}
	return nil
}

func hashOK(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}

// Write publishes a complete pack by renaming a temporary directory. Existing
// nonempty outputs are refused; callers select a fresh build/staging directory.
func Write(dir string, d *english.Dictionary, m Manifest, notice []byte) error {
	m.Format, m.Words = 1, d.Len()
	binary := d.Encode()
	m.Files = map[string]File{"words.bin": {Sum(binary), int64(len(binary))}, "NOTICE.txt": {Sum(notice), int64(len(notice))}}
	if err := m.validate(); err != nil {
		return err
	}
	if _, err := english.Decode(binary); err != nil {
		return err
	}
	if strings.TrimSpace(string(notice)) == "" {
		return fmt.Errorf("英文授權聲明不可空白")
	}
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("輸出不接受符號連結")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("輸出已存在，請使用新目錄：%s", dir)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".0c-english-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mb = append(mb, '\n')
	for name, data := range map[string][]byte{"words.bin": binary, "NOTICE.txt": notice, "manifest.json": mb} {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0644); err != nil {
			return err
		}
	}
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmp, dir)
}
