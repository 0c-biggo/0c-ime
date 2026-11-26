package englishpack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
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
	v, err := readVocabulary(src)
	if err != nil {
		return err
	}
	words := append(slices.Clone(v.esdb), v.technical...)
	d := english.NewFiltered(words, v.blocked)
	m := Manifest{Ranking: "ESDB size prior (100-size), with authored product vocabulary priors; not corpus frequency", Sources: []Source{v.source.Source}, Inputs: map[string]string{}}
	for name, b := range v.inputs {
		m.Inputs[name] = Sum(b)
	}
	return Write(out, d, m, v.notice("排序採 size 先驗及本專案自行編列的產品詞表初值，並非語料詞頻。"))
}

// vocabulary 是已核對雜湊的 ESDB 與技術詞表（兩種資料包共用）。
type vocabulary struct {
	inputs    map[string][]byte
	source    SourceMetadata
	esdb      []english.Word // 權重 100-size
	blocked   []string
	technical []english.Word
	// technicalDisplay 是 technical.tsv 第三欄的常見寫法（詞頻資料包用）。
	technicalDisplay map[string]string
}

func readVocabulary(src string) (*vocabulary, error) {
	v := &vocabulary{inputs: map[string][]byte{}}
	for _, name := range []string{"source.json", "esdb.tsv", "technical.tsv", "LICENSE.txt"} {
		b, err := read(filepath.Join(src, name), 16<<20)
		if err != nil {
			return nil, err
		}
		v.inputs[name] = b
	}
	if err := json.Unmarshal(v.inputs["source.json"], &v.source); err != nil {
		return nil, err
	}
	if Sum(v.inputs["esdb.tsv"]) != v.source.NormalizedSHA256 || Sum(v.inputs["LICENSE.txt"]) != v.source.LicenseSHA256 {
		return nil, fmt.Errorf("英文來源詞表或授權檔雜湊不符")
	}
	if strings.TrimSpace(string(v.inputs["LICENSE.txt"])) == "" {
		return nil, fmt.Errorf("英文來源缺少授權文字")
	}
	scan := bufio.NewScanner(bytes.NewReader(v.inputs["esdb.tsv"]))
	previous := ""
	for scan.Scan() {
		f := strings.Split(scan.Text(), "\t")
		if len(f) != 3 || f[0] <= previous || !lowerWord(f[0]) {
			return nil, fmt.Errorf("esdb.tsv 詞條內容或排序不合法")
		}
		grade, err := strconv.Atoi(f[1])
		if err != nil || !validSize(grade) || (f[2] != "0" && f[2] != "1") {
			return nil, fmt.Errorf("esdb.tsv 等級或建議標記不合法")
		}
		v.esdb = append(v.esdb, english.Word{Text: f[0], Weight: float64(100 - grade)})
		if f[2] == "1" {
			v.blocked = append(v.blocked, f[0])
		}
		previous = f[0]
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if len(v.esdb) != v.source.Words || len(v.esdb) == 0 || len(v.esdb) > english.MaxPackWords {
		return nil, fmt.Errorf("英文來源詞數不符")
	}
	scan = bufio.NewScanner(bytes.NewReader(v.inputs["technical.tsv"]))
	seen := map[string]bool{}
	for scan.Scan() {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 && len(f) != 3 || !lowerWord(f[0]) || seen[f[0]] {
			return nil, fmt.Errorf("technical.tsv 詞條不合法或重複")
		}
		// 第三欄（選填）是常見寫法（Monday、AirPods、PDF），只有詞頻資料包用、語料沒有寫法時才用；
		// 桌面資料包照舊只看小寫。
		if len(f) == 3 {
			if f[2] == f[0] || strings.ToLower(f[2]) != f[0] {
				return nil, fmt.Errorf("technical.tsv 的寫法 %q 不是 %q 的大小寫變化", f[2], f[0])
			}
			if v.technicalDisplay == nil {
				v.technicalDisplay = map[string]string{}
			}
			v.technicalDisplay[f[0]] = f[2]
		}
		weight, err := strconv.ParseFloat(f[1], 64)
		if err != nil || weight <= 0 || weight > 1000 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return nil, fmt.Errorf("technical.tsv 權重不合法")
		}
		seen[f[0]] = true
		v.technical = append(v.technical, english.Word{Text: f[0], Weight: weight})
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *vocabulary) notice(ranking string) []byte {
	source := v.source.Source
	return append([]byte(fmt.Sprintf("靈犀注音英文詞表\n\n來源：%s\n版本：%s\n網址：%s\n來源 SHA256：%s\n\n選用 A/B/Z、size ≤ 60、variant ≤ 1；保留小寫 ASCII 字母及上游 no-suggest 標記。\n%s\n\n", source.Name, source.Version, source.URL, source.SHA256, ranking)), v.inputs["LICENSE.txt"]...)
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
