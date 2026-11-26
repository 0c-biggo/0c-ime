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
	for _, name := range []string{"words.bin", "NOTICE.txt"} { // 格式 2 的 bigram.bin 由 LoadBigrams 讀
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

// packFiles 是各格式的檔案：格式 1 是桌面的詞表，格式 2 是詞頻資料包（ranked.go），多一份詞對。
func packFiles(format int) []string {
	switch format {
	case 1:
		return []string{"words.bin", "NOTICE.txt"}
	case 2:
		return []string{"words.bin", "bigram.bin", "NOTICE.txt"}
	}
	return nil
}

func (m Manifest) validate() error {
	names := packFiles(m.Format)
	if names == nil || m.Words < 1 || m.Words > english.MaxPackWords || m.Ranking == "" || len(m.Sources) == 0 || len(m.Files) != len(names) {
		return fmt.Errorf("英文 manifest 格式不合法")
	}
	for _, name := range names {
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
	if d.Ranked() {
		return fmt.Errorf("詞頻字典請用 CompileRanked")
	}
	m.Format, m.Words = 1, d.Len()
	binary := d.Encode()
	if _, err := english.Decode(binary); err != nil {
		return err
	}
	return publish(dir, m, map[string][]byte{"words.bin": binary, "NOTICE.txt": notice})
}

func writeRanked(dir string, d *english.Dictionary, b *english.Bigrams, m Manifest, notice []byte) error {
	m.Format, m.Words = 2, d.Len()
	words, pairs := d.Encode(), b.Encode()
	if back, err := english.Decode(words); err != nil || !back.Ranked() || back.Len() != d.Len() {
		return fmt.Errorf("詞頻字典編碼後讀不回來：%v", err)
	}
	if back, err := english.DecodeBigrams(pairs); err != nil || back.Len() != b.Len() {
		return fmt.Errorf("英文詞對編碼後讀不回來：%v", err)
	}
	return publish(dir, m, map[string][]byte{"words.bin": words, "bigram.bin": pairs, "NOTICE.txt": notice})
}

func publish(dir string, m Manifest, files map[string][]byte) error {
	m.Files = map[string]File{}
	for name, data := range files {
		m.Files[name] = File{Sum(data), int64(len(data))}
	}
	if err := m.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(files["NOTICE.txt"])) == "" {
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
	files["manifest.json"] = append(mb, '\n')
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0644); err != nil {
			return err
		}
	}
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmp, dir)
}

// LoadBigrams 讀詞頻資料包（格式 2）的英文與跨中英詞對；transform 同 LoadWithTransform。
func LoadBigrams(dir string, transform func([]byte) ([]byte, error)) (*english.Bigrams, error) {
	b, err := read(filepath.Join(dir, "manifest.json"), 64<<10)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if err = m.validate(); err != nil {
		return nil, err
	}
	if m.Format != 2 {
		return nil, fmt.Errorf("英文資料包沒有詞對")
	}
	data, err := read(filepath.Join(dir, "bigram.bin"), english.MaxPackBytes+64)
	if err != nil {
		return nil, err
	}
	if transform != nil {
		if data, err = transform(data); err != nil {
			return nil, err
		}
	}
	if info := m.Files["bigram.bin"]; info.Size != int64(len(data)) || info.SHA256 != Sum(data) {
		return nil, fmt.Errorf("英文 bigram.bin 與 manifest 不符")
	}
	return english.DecodeBigrams(data)
}
