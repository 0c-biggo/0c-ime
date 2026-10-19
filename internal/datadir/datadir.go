// Package datadir 從資料目錄載入引擎所需的資料。讀檔是宿主的責任，所以不在 core 裡。
// M0 讀來源格式的 TSV；M1 改為 0c-compile 產生的資料包。
package datadir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/english"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/internal/englishpack"
)

// Open 載入資料目錄。有 manifest.json 時視為 0c-compile 產生的資料包（驗證雜湊）；
// 否則讀取來源格式的 lexicon.tsv 與 lm.tsv（玩具詞庫、測試）。
func Open(dir string) (*engine.Engine, error) {
	eng, err := openChinese(dir)
	if err != nil {
		return nil, err
	}
	eng.English = english.Prototype()
	path := filepath.Join(dir, "english")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return eng, nil
	} else if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("英文資料包必須是一般目錄")
	}
	// Old full-package rollback removes optional English files individually and
	// can leave an empty directory. A partial pack still fails verification.
	if entries, err := os.ReadDir(path); err == nil && len(entries) == 0 {
		return eng, nil
	}
	d, m, err := englishpack.LoadWithTransform(path, unseal)
	if err != nil {
		return nil, fmt.Errorf("english：%w", err)
	}
	eng.English = d
	eng.EnglishPack = m.Sources[0].Version
	return eng, nil
}

func openChinese(dir string) (*engine.Engine, error) {
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil {
		return openPack(dir)
	}
	lf, err := os.Open(filepath.Join(dir, "lexicon.tsv"))
	if err != nil {
		return nil, err
	}
	defer lf.Close()
	lex, err := lexicon.Load(lf)
	if err != nil {
		return nil, fmt.Errorf("lexicon.tsv：%w", err)
	}
	mf, err := os.Open(filepath.Join(dir, "lm.tsv"))
	if err != nil {
		return nil, err
	}
	defer mf.Close()
	sys, err := lm.Load(mf)
	if err != nil {
		return nil, fmt.Errorf("lm.tsv：%w", err)
	}
	return engine.New(lex, sys), nil
}

func openPack(dir string) (*engine.Engine, error) {
	mb, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m struct {
		Format int `json:"format"`
		Files  map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"files"`
		Runtime *runtimeConfig `json:"runtime,omitempty"`
	}
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, fmt.Errorf("manifest.json：%w", err)
	}
	if m.Format != 1 && m.Format != 2 {
		return nil, fmt.Errorf("資料包格式 %d 不受支援", m.Format)
	}
	if (m.Format == 2) != (m.Runtime != nil) {
		return nil, fmt.Errorf("執行參數須使用格式 2；格式 1 維持原預設")
	}
	if err := m.Runtime.validate(); err != nil {
		return nil, fmt.Errorf("manifest runtime：%w", err)
	}
	read := func(name string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if b, err = unseal(b); err != nil {
			return nil, fmt.Errorf("%s：%w", name, err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != m.Files[name].SHA256 {
			return nil, fmt.Errorf("%s 與 manifest 的雜湊不符", name)
		}
		return b, nil
	}
	lb, err := read("lexicon.bin")
	if err != nil {
		return nil, err
	}
	lex, err := lexicon.Decode(lb)
	if err != nil {
		return nil, fmt.Errorf("lexicon.bin：%w", err)
	}
	mbin, err := read("lm.bin")
	if err != nil {
		return nil, err
	}
	sys, err := lm.DecodeCompact(mbin)
	if err != nil {
		return nil, fmt.Errorf("lm.bin：%w", err)
	}
	eng := engine.New(lex, sys)
	if m.Runtime != nil {
		eng.DocumentContextWeight = m.Runtime.DocumentContextWeight
	}
	return eng, nil
}

// runtimeConfig 只包含已校準且能明確版本化的執行參數。
// 舊資料包不含此欄位，維持原本預設；零是明確停用前文，不能當作缺省。
type runtimeConfig struct {
	Version               int      `json:"version"`
	DocumentContextWeight *float64 `json:"document_context_weight"`
}

func (c *runtimeConfig) validate() error {
	if c == nil {
		return nil
	}
	if c.Version != 1 || c.DocumentContextWeight == nil {
		return fmt.Errorf("不支援的版本或缺少前文權重")
	}
	w := *c.DocumentContextWeight
	if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 || w > 1 {
		return fmt.Errorf("前文權重須介於 0 與 1")
	}
	return nil
}

// LoadUser 載入個人資料；檔案不存在時從空白開始。
func LoadUser(e *engine.Engine, path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := personal.LoadStore(f)
	if err != nil {
		return fmt.Errorf("%s：%w", path, err)
	}
	e.Model.Store = st
	return nil
}

// SaveUser 先寫暫存檔、落盤後再改名，中斷或斷電時保留上一份可用資料。
// 存檔前每日清理一次衰減到不影響排序的項目，檔案大小有上界。
func SaveUser(e *engine.Engine, path string) error {
	e.PrunePersonalIfDue()
	snap := e.PersonalSnapshot()
	return writeAtomic(path, snap.Save)
}

// writeAtomic 先寫暫存檔、落盤後再改名。
func writeAtomic(path string, write func(io.Writer) error) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	// 改名前不落盤，斷電後可能只剩改過名的空檔或半個檔。
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// VocabFileName 是自訂詞彙檔（#122），與 user.json 放在同一個目錄。
const VocabFileName = "vocab.json"

// VocabPath 回傳個人資料檔旁邊的自訂詞彙檔路徑。
func VocabPath(userPath string) string { return filepath.Join(filepath.Dir(userPath), VocabFileName) }

// LoadVocab 載入自訂詞彙；檔案不存在時不動引擎。較新版本寫成的檔案回傳 *vocab.NewerFormatError，
// 呼叫端不得再寫回（照常輸入，只是沒有自訂詞）。
func LoadVocab(e *engine.Engine, path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	v, err := vocab.Load(f)
	if err != nil {
		return fmt.Errorf("%s：%w", path, err)
	}
	e.SetVocab(v)
	return nil
}

// SaveVocab 寫出自訂詞彙；引擎沒有載入或建立過自訂詞彙時不寫。
func SaveVocab(e *engine.Engine, path string) error {
	snap := e.VocabSnapshot()
	if snap == nil {
		return nil
	}
	return writeAtomic(path, snap.Save)
}

// syncDir 盡量讓改名本身也落盤。資料已在新檔，失敗只代表斷電後可能看到上一份，
// 所以不回報錯誤：部分檔案系統不支援對目錄 fsync，回報會讓每次存檔都失敗、引擎交接卡住。
// Windows 不能對目錄 fsync，NTFS 日誌會保留改名前或改名後其中一份。
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}
