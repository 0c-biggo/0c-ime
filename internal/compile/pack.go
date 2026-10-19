package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manifest 描述一份資料包：檔案雜湊、規模、來源與授權。引擎載入時驗證雜湊。
type Manifest struct {
	Format  int                 `json:"format"`
	Files   map[string]FileInfo `json:"files"`
	Counts  map[string]int      `json:"counts"`
	Options Options             `json:"options"`
	Sources []SourceInfo        `json:"sources"`
}

type FileInfo struct {
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

type SourceInfo struct {
	ID, Name, URL, Version, License, Attribution string
	Distributable                                bool
}

// Write 把編譯結果寫成資料包目錄：lexicon.bin、lm.bin、manifest.json、NOTICE.txt、report.txt。
// licenseDir 內的 <來源 ID>.txt 會原文附進 NOTICE（例如 MIT 要求保留全文）。
func (r *Result) Write(out, licenseDir string, o Options) error {
	if len(r.Errors) > 0 {
		return fmt.Errorf("有 %d 個錯誤，不出版", len(r.Errors))
	}
	// 在建立或改寫輸出前檢查，失敗時既有資料包保持原樣。
	if o.Public {
		if err := r.checkPublicSources(licenseDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	m := Manifest{Format: 1, Files: map[string]FileInfo{}, Options: o, Counts: map[string]int{
		"entries": r.Report.Entries, "words": r.Report.Words, "counted_words": r.Report.CountedWords, "bigrams": r.Report.Bigrams,
		"fallback_words": r.Report.FallbackWords,
	}}
	for name, b := range map[string][]byte{"lexicon.bin": r.Lexicon.Encode(), "lm.bin": r.LM.Encode()} {
		sum := sha256.Sum256(b)
		m.Files[name] = FileInfo{hex.EncodeToString(sum[:]), len(b)}
		if err := writeAtomic(filepath.Join(out, name), b); err != nil {
			return err
		}
	}
	var notice strings.Builder
	notice.WriteString("靈犀注音（0c）詞庫資料包使用下列來源，各來源依其授權條款使用。\n")
	for _, s := range r.Sources {
		m.Sources = append(m.Sources, SourceInfo{s.ID, s.Name, s.URL, s.Version, s.License, s.Attribution, s.Distributable})
		fmt.Fprintf(&notice, "\n== %s（%s）==\n授權：%s\n版本：%s\n網址：%s\n", s.Name, s.ID, s.License, s.Version, s.URL)
		if s.Attribution != "" && s.Attribution != "-" {
			fmt.Fprintf(&notice, "姓名標示：%s\n", s.Attribution)
		}
		if b, err := os.ReadFile(filepath.Join(licenseDir, s.ID+".txt")); err == nil {
			fmt.Fprintf(&notice, "\n%s\n", strings.TrimSpace(string(b)))
		}
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	for name, b := range map[string][]byte{"manifest.json": mb, "NOTICE.txt": []byte(notice.String()), "report.txt": []byte(r.Report.String())} {
		if err := writeAtomic(filepath.Join(out, name), b); err != nil {
			return err
		}
	}
	return nil
}

// checkPublicSources 只檢查來源帳本與聲明是否完整，不替代來源授權審查。
func (r *Result) checkPublicSources(licenseDir string) error {
	if len(r.Sources) == 0 {
		return fmt.Errorf("公開包沒有來源紀錄")
	}
	for _, s := range r.Sources {
		if s.ID == "" || s.ID == "." || s.ID == ".." || strings.ContainsAny(s.ID, `/\\`) {
			return fmt.Errorf("公開包來源 ID 無效：%q", s.ID)
		}
		if !s.Distributable {
			return fmt.Errorf("公開包來源 %s 標示為不可散布", s.ID)
		}
		for _, field := range []struct{ name, value string }{
			{"名稱", s.Name}, {"網址", s.URL}, {"版本", s.Version},
			{"授權", s.License}, {"署名", s.Attribution},
		} {
			value := strings.TrimSpace(field.value)
			if value == "" || value == "-" || value == "未定" || strings.EqualFold(value, "unknown") {
				return fmt.Errorf("公開包來源 %s 缺少%s", s.ID, field.name)
			}
		}
		b, err := os.ReadFile(filepath.Join(licenseDir, s.ID+".txt"))
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			return fmt.Errorf("公開包來源 %s 缺少授權聲明：%s.txt", s.ID, s.ID)
		}
	}
	return nil
}

func writeAtomic(p string, b []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
