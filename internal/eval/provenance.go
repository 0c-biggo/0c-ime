package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
)

func fileHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func ProvenanceData(path, fallback string) map[string]any {
	if path == "" {
		path = fallback
	}
	return map[string]any{"path": path, "manifest_sha256": fileHash(filepath.Join(path, "manifest.json"))}
}

// 執行檔雜湊可辨識同 commit 的不同未提交修改；不讀 .env 或個人資料。
func Provenance(data string) map[string]any {
	m := ProvenanceData(data, data)
	if path, err := os.Executable(); err == nil {
		m["executable_sha256"] = fileHash(path)
	}
	if b, ok := debug.ReadBuildInfo(); ok {
		m["go"] = b.GoVersion
		for _, s := range b.Settings {
			if s.Key == "vcs.revision" || s.Key == "vcs.modified" {
				m[s.Key] = s.Value
			}
		}
	}
	return m
}

func WriteReport(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
