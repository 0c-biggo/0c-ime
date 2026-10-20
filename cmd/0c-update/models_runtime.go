package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/0c-biggo/0c-ime/internal/atomicfile"
)

// 系統 Python 能不能跑 worker（#180）：import numpy、onnxruntime 要零點幾秒到一秒多，設定程式在下載中每秒輪詢
// status，不能每次都跑。結果記在個人目錄的 models/runtime.json，以 Python 執行檔與它的 sys.path 每個目錄的
// 修改時間為鍵：apt 裝上或移除 python3-onnxruntime 會在 dist-packages 新增或刪掉目錄，鍵就變了，馬上重新檢查。
// 原地升級套件不一定改到目錄的時間，所以快取最多用 runtimeCacheTTL。

const (
	runtimeCacheFile = "runtime.json"
	runtimeCacheTTL  = 10 * time.Minute
)

// runtimeProbe 是一次實際檢查的結果。
type runtimeProbe struct {
	Missing []string `json:"missing"` // 缺少的 Debian 套件名
	Path    []string `json:"path"`    // 這個 Python 的 sys.path（-I）
}

type runtimeCache struct {
	Schema    int       `json:"schema"`
	Python    string    `json:"python"`
	Key       string    `json:"key"`
	Path      []string  `json:"path"`
	Missing   []string  `json:"missing"`
	CheckedAt time.Time `json:"checked_at"`
}

// runtimeKey：Python 執行檔（解開連結後的路徑、大小、修改時間）與 sys.path 每個目錄的修改時間。
func runtimeKey(python string, paths []string) string {
	h := sha256.New()
	stamp := func(label, p string) {
		real := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			real = r
		}
		if st, err := os.Stat(real); err == nil {
			fmt.Fprintf(h, "%s %s %s %d %d\n", label, p, real, st.Size(), st.ModTime().UnixNano())
		} else {
			fmt.Fprintf(h, "%s %s missing\n", label, p)
		}
	}
	stamp("python", python)
	for _, p := range paths {
		stamp("path", p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cachedModelRuntime 回傳缺少的套件：快取的鍵相符而且還沒過期時直接用，否則以 probe 檢查並記下。
// probe 失敗（例如逾時）時照實回報、不記，下一次再檢查。
func cachedModelRuntime(dir, python string, now time.Time, probe func(string) (runtimeProbe, error)) []string {
	file := filepath.Join(dir, runtimeCacheFile)
	var c runtimeCache
	if b, err := os.ReadFile(file); err == nil && json.Unmarshal(b, &c) == nil && c.Schema == 1 && c.Python == python &&
		c.Missing != nil && !now.Before(c.CheckedAt) && now.Sub(c.CheckedAt) < runtimeCacheTTL && c.Key == runtimeKey(python, c.Path) {
		return c.Missing
	}
	p, err := probe(python)
	if err != nil {
		return p.Missing
	}
	if p.Missing == nil {
		p.Missing = []string{}
	}
	if os.MkdirAll(dir, 0o700) == nil {
		_ = atomicfile.WriteJSON(file, runtimeCache{Schema: 1, Python: python, Key: runtimeKey(python, p.Path), Path: p.Path,
			Missing: p.Missing, CheckedAt: now.UTC()})
	}
	return p.Missing
}
