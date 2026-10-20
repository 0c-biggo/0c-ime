package englishpack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/0c-biggo/0c-ime/core/english"
)

type SourceMetadata struct {
	Source           Source   `json:"source"`
	NormalizedSHA256 string   `json:"normalized_sha256"`
	LicenseSHA256    string   `json:"license_sha256"`
	Words            int      `json:"words"`
	Exports          []Export `json:"exports"`
}
type Export struct {
	Size   int    `json:"size"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// Compile consumes reviewed, hash-pinned product vocabulary. It never fetches
// data, reads a user's words or invokes training tools.
func Compile(src, out string) error {
	inputs := map[string][]byte{}
	for _, name := range []string{"source.json", "esdb.tsv", "technical.tsv", "LICENSE.txt"} {
		b, err := read(filepath.Join(src, name), 16<<20)
		if err != nil {
			return err
		}
		inputs[name] = b
	}
	var source SourceMetadata
	if err := json.Unmarshal(inputs["source.json"], &source); err != nil {
		return err
	}
	if Sum(inputs["esdb.tsv"]) != source.NormalizedSHA256 || Sum(inputs["LICENSE.txt"]) != source.LicenseSHA256 {
		return fmt.Errorf("英文來源詞表或授權檔雜湊不符")
	}
	if strings.TrimSpace(string(inputs["LICENSE.txt"])) == "" {
		return fmt.Errorf("英文來源缺少授權文字")
	}
	var words []english.Word
	var blocked []string
	scan := bufio.NewScanner(bytes.NewReader(inputs["esdb.tsv"]))
	previous := ""
	for scan.Scan() {
		f := strings.Split(scan.Text(), "\t")
		if len(f) != 3 || f[0] <= previous || !lowerWord(f[0]) {
			return fmt.Errorf("esdb.tsv 詞條內容或排序不合法")
		}
		grade, err := strconv.Atoi(f[1])
		if err != nil || !validSize(grade) || (f[2] != "0" && f[2] != "1") {
			return fmt.Errorf("esdb.tsv 等級或建議標記不合法")
		}
		words = append(words, english.Word{Text: f[0], Weight: float64(100 - grade)})
		if f[2] == "1" {
			blocked = append(blocked, f[0])
		}
		previous = f[0]
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if len(words) != source.Words || len(words) == 0 || len(words) > english.MaxPackWords {
		return fmt.Errorf("英文來源詞數不符")
	}
	scan = bufio.NewScanner(bytes.NewReader(inputs["technical.tsv"]))
	seen := map[string]bool{}
	for scan.Scan() {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 || !lowerWord(f[0]) || seen[f[0]] {
			return fmt.Errorf("technical.tsv 詞條不合法或重複")
		}
		weight, err := strconv.ParseFloat(f[1], 64)
		if err != nil || weight <= 0 || weight > 1000 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return fmt.Errorf("technical.tsv 權重不合法")
		}
		seen[f[0]] = true
		words = append(words, english.Word{Text: f[0], Weight: weight})
	}
	if err := scan.Err(); err != nil {
		return err
	}
	d := english.NewFiltered(words, blocked)
	m := Manifest{Ranking: "ESDB size prior (100-size), with authored product vocabulary priors; not corpus frequency", Sources: []Source{source.Source}, Inputs: map[string]string{}}
	for name, b := range inputs {
		m.Inputs[name] = Sum(b)
	}
	notice := append([]byte(fmt.Sprintf("靈犀注音英文詞表\n\n來源：%s\n版本：%s\n網址：%s\n來源 SHA256：%s\n\n選用 A/B/Z、size ≤ 60、variant ≤ 1；保留小寫 ASCII 字母及上游 no-suggest 標記。\n排序採 size 先驗及本專案自行編列的產品詞表初值，並非語料詞頻。\n\n", source.Source.Name, source.Source.Version, source.Source.URL, source.Source.SHA256)), inputs["LICENSE.txt"]...)
	return Write(out, d, m, notice)
}

func lowerWord(s string) bool {
	if len(s) < 1 || len(s) > english.MaxWordBytes {
		return false
	}
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
func validSize(n int) bool {
	switch n {
	case 10, 20, 35, 40, 50, 55, 60:
		return true
	}
	return false
}
