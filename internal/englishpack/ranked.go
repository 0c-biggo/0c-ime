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

// 詞頻資料包（格式 2，APP#21）：ESDB 與技術詞表的詞，加上 data/english/frequency 的語料統計
// （英文詞頻、台灣常用英文、英文詞對、跨中英詞對）。手機與
// 桌面（#358）都出貨這一種；Compile（格式 1）的輸出逐位元組不變。

// FrequencySource 是 data/english/frequency/source.json：固定 models 產物 manifest 的雜湊，
// manifest 再列出每份 TSV 的雜湊與大小。
type FrequencySource struct {
	Name           string `json:"name"`
	Artifact       string `json:"artifact"`
	Schema         string `json:"schema"`
	URL            string `json:"url"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Attribution    string `json:"attribution"`
}

type frequencyManifest struct {
	Schema string `json:"schema"`
	Files  map[string]struct {
		Bytes  int64  `json:"bytes"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
	Splits struct {
		Train struct {
			News struct {
				Tokens int64 `json:"total_english_tokens"`
			} `json:"news"`
			Social struct {
				Tokens int64 `json:"total_english_tokens"`
			} `json:"social"`
		} `json:"train"`
	} `json:"splits"`
}

var frequencyFiles = []string{"english-unigram.tsv", "taiwan-unigram.tsv", "english-bigram.tsv", "mixed-bigram.tsv"}

// 權重換算（english.Entry.Weight）：
//   - 語料裡有的詞：兩份語料每百萬詞的次數加權平均（rankedNewsShare），
//     每百萬 1 次是 55 分，每多 10 倍加 10 分（the 約 101）。語料最少見的詞約 55。
//   - 語料沒有的 ESDB 詞：照 size 給 45（35）到 35（60），都低於語料裡的詞。
//   - 技術詞表新增、語料沒有的詞：50。技術詞表的人工先驗在這裡不用，排序只看詞頻。
const (
	rankedNewsShare  = 0.7
	rankedBase       = 55
	rankedPerDecade  = 10
	rankedTechnical  = 50
	rankedTitleShare = 0.7
	rankedOtherShare = 0.5
)

func esdbRankedWeight(sizePrior float64) float64 { return 45 - float64((65-sizePrior)*0.4) }

// rankedWeight 取到小數第四位：math.Log10 在 amd64 與 arm64 的最後一位可能不同，取整後
// 兩邊編出的資料包逐位元組相同（手機資料在 x86 的 Android 建置機與 arm64 的 Mac 上各編一次）。
func rankedWeight(perMillion float64) float64 {
	return math.Round(float64(rankedBase+rankedPerDecade*math.Log10(perMillion))*1e4) / 1e4
}

// CompileRanked 編出詞頻資料包：words.bin（格式 2）、bigram.bin、NOTICE.txt、manifest.json。
func CompileRanked(src, out string) error {
	v, err := readVocabulary(src)
	if err != nil {
		return err
	}
	fsrc, tables, err := readFrequency(filepath.Join(src, "frequency"))
	if err != nil {
		return err
	}
	displayFile, err := read(filepath.Join(src, "display.tsv"), 1<<20)
	if err != nil {
		return err
	}
	display, err := readDisplay(displayFile, v.technicalDisplay)
	if err != nil {
		return err
	}
	d, b, err := buildRanked(v, tables, display)
	if err != nil {
		return err
	}
	m := Manifest{
		Ranking: "corpus frequency per million tokens (55 + 10*log10); ESDB words outside the corpus 35-45 by size; display forms, contractions and the Taiwan list from the corpus",
		Sources: []Source{v.source.Source, {Name: fsrc.Name, Version: fsrc.Artifact, URL: fsrc.URL, SHA256: fsrc.ManifestSHA256, License: "自有語料統計", Attribution: fsrc.Attribution}},
		Inputs:  map[string]string{},
	}
	for name, data := range v.inputs {
		m.Inputs[name] = Sum(data)
	}
	for name, data := range tables.inputs {
		m.Inputs["frequency/"+name] = Sum(data)
	}
	m.Inputs["display.tsv"] = Sum(displayFile)
	notice := v.notice(fmt.Sprintf("排序採語料詞頻：%s（%s），%s；只含統計次數，不含原文。", fsrc.Name, fsrc.Artifact, fsrc.Attribution))
	return writeRanked(out, d, b, m, notice)
}

type frequencyTables struct {
	inputs               map[string][]byte
	newsTokens, social   float64
	unigram, taiwan      []frequencyRow
	englishPairs, zhPair []english.Pair
	enZhPairs            []english.Pair
}

type frequencyRow struct {
	key      string
	count    float64
	variants [][2]any
	esdb     bool
}

func readFrequency(dir string) (*FrequencySource, *frequencyTables, error) {
	sb, err := read(filepath.Join(dir, "source.json"), 64<<10)
	if err != nil {
		return nil, nil, err
	}
	var src FrequencySource
	if err = json.Unmarshal(sb, &src); err != nil {
		return nil, nil, err
	}
	if src.Name == "" || src.Artifact == "" || src.URL == "" || src.Attribution == "" || !hashOK(src.ManifestSHA256) || src.Schema != "0c-english-source-stats-v1" {
		return nil, nil, fmt.Errorf("英文詞頻來源說明不完整")
	}
	mb, err := read(filepath.Join(dir, "manifest.json"), 1<<20)
	if err != nil {
		return nil, nil, err
	}
	if Sum(mb) != src.ManifestSHA256 {
		return nil, nil, fmt.Errorf("英文詞頻 manifest 雜湊不符")
	}
	var m frequencyManifest
	if err = json.Unmarshal(mb, &m); err != nil {
		return nil, nil, err
	}
	if m.Schema != src.Schema || m.Splits.Train.News.Tokens <= 0 || m.Splits.Train.Social.Tokens <= 0 {
		return nil, nil, fmt.Errorf("英文詞頻 manifest 格式不合法")
	}
	t := &frequencyTables{inputs: map[string][]byte{"source.json": sb, "manifest.json": mb},
		newsTokens: float64(m.Splits.Train.News.Tokens), social: float64(m.Splits.Train.Social.Tokens)}
	for _, name := range frequencyFiles {
		b, err := read(filepath.Join(dir, name), 16<<20)
		if err != nil {
			return nil, nil, err
		}
		f, ok := m.Files[name]
		if !ok || f.Bytes != int64(len(b)) || f.SHA256 != Sum(b) {
			return nil, nil, fmt.Errorf("英文詞頻 %s 與 manifest 不符", name)
		}
		t.inputs[name] = b
	}
	if t.unigram, err = readUnigram(t.inputs["english-unigram.tsv"], []string{"word", "count", "documents", "variants_json"}); err != nil {
		return nil, nil, err
	}
	if t.taiwan, err = readUnigram(t.inputs["taiwan-unigram.tsv"], []string{"word", "count", "documents", "communities", "in_esdb", "variants_json"}); err != nil {
		return nil, nil, err
	}
	err = eachRow(t.inputs["english-bigram.tsv"], []string{"left", "right", "count", "documents"}, func(f []string) error {
		n, err := count(f[2])
		t.englishPairs = append(t.englishPairs, english.Pair{Left: f[0], Right: f[1], Count: n})
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	err = eachRow(t.inputs["mixed-bigram.tsv"], []string{"direction", "left", "right", "count", "documents"}, func(f []string) error {
		n, err := count(f[3])
		switch f[0] {
		case "zh-en":
			t.zhPair = append(t.zhPair, english.Pair{Left: f[1], Right: f[2], Count: n})
		case "en-zh":
			t.enZhPairs = append(t.enZhPairs, english.Pair{Left: f[1], Right: f[2], Count: n})
		default:
			return fmt.Errorf("跨中英詞對方向不合法：%q", f[0])
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return &src, t, nil
}

func count(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("英文詞頻次數不合法：%q", s)
	}
	return uint32(n), nil
}

func eachRow(data []byte, header []string, fn func([]string) error) error {
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	first := true
	for scan.Scan() {
		f := strings.Split(scan.Text(), "\t")
		if first {
			if !slices.Equal(f, header) {
				return fmt.Errorf("英文詞頻欄名不符：%v", f)
			}
			first = false
			continue
		}
		if len(f) != len(header) {
			return fmt.Errorf("英文詞頻欄數不符：%q", scan.Text())
		}
		if err := fn(f); err != nil {
			return err
		}
	}
	if first {
		return fmt.Errorf("英文詞頻缺欄名")
	}
	return scan.Err()
}

func readUnigram(data []byte, header []string) ([]frequencyRow, error) {
	var rows []frequencyRow
	seen := map[string]bool{}
	err := eachRow(data, header, func(f []string) error {
		n, err := count(f[1])
		if err != nil {
			return err
		}
		row := frequencyRow{key: f[0], count: float64(n)}
		if seen[row.key] || row.key != strings.ToLower(row.key) {
			return fmt.Errorf("英文詞頻詞條重複或不是小寫：%q", row.key)
		}
		seen[row.key] = true
		if len(header) == 6 {
			row.esdb = f[4] == "1"
		}
		if err := json.Unmarshal([]byte(f[len(f)-1]), &row.variants); err != nil {
			return fmt.Errorf("英文詞頻寫法不合法：%q", f[0])
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

// readDisplay 讀 display.tsv：只補常見寫法的詞（Taiwan、Netflix、DRAM，#358）。每行「小寫詞<TAB>寫法」，
// 寫法只能跟詞差大小寫；詞不能重複，也不能已經在 technical.tsv 第三欄有寫法（同一個詞只有一個手寫來源）。
// 詞在不在字典裡由 buildRanked 檢查。只有詞頻資料包讀這個檔，格式 1 不受影響。
func readDisplay(data []byte, technical map[string]string) (map[string]string, error) {
	display := map[string]string{}
	scan := bufio.NewScanner(bytes.NewReader(data))
	for scan.Scan() {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 2 || !english.RankedKey(f[0]) {
			return nil, fmt.Errorf("display.tsv 詞條不合法：%q", line)
		}
		if f[1] == f[0] || strings.ToLower(f[1]) != f[0] {
			return nil, fmt.Errorf("display.tsv 的寫法 %q 不是 %q 的大小寫變化", f[1], f[0])
		}
		if _, dup := display[f[0]]; dup {
			return nil, fmt.Errorf("display.tsv 的 %q 重複", f[0])
		}
		if _, dup := technical[f[0]]; dup {
			return nil, fmt.Errorf("display.tsv 的 %q 已在 technical.tsv 第三欄有寫法", f[0])
		}
		display[f[0]] = f[1]
	}
	return display, scan.Err()
}

// buildRanked 合併詞表與詞頻。語料裡不是「字母與撇號」的詞（t1、ps5，146 個台灣常用詞）不收：
// 含數字的英數詞另由 core/english/alnum.tsv 處理。display 是 display.tsv 的寫法：只設寫法，
// 不加詞、不動權重與旗標（混打認不認照舊）；詞不在字典裡就是錯誤。
func buildRanked(v *vocabulary, t *frequencyTables, display map[string]string) (*english.Dictionary, *english.Bigrams, error) {
	type stat struct {
		perMillion float64
		variants   map[string]float64
		protected  bool
	}
	stats := map[string]*stat{}
	add := func(rows []frequencyRow, share, tokens float64, protected bool) {
		for _, r := range rows {
			if !english.RankedKey(r.key) {
				continue
			}
			s := stats[r.key]
			if s == nil {
				s = &stat{variants: map[string]float64{}}
				stats[r.key] = s
			}
			// 明寫 float64() 不讓 arm64 把乘法與加法融合（FMA），資料包才在各平台逐位元組相同。
			s.perMillion += float64(share * r.count / tokens * 1e6)
			s.protected = s.protected || protected
			for _, v := range r.variants {
				form, ok := v[0].(string)
				n, _ := v[1].(float64)
				if ok && n > 0 && strings.ToLower(form) == r.key {
					s.variants[form] += float64(share * n / tokens * 1e6)
				}
			}
		}
	}
	add(t.unigram, rankedNewsShare, t.newsTokens, false)
	add(t.taiwan, 1-rankedNewsShare, t.social, true)

	entries := map[string]*english.Entry{}
	general := map[string]bool{} // ESDB 的一般英文詞
	for _, w := range v.esdb {
		entries[w.Text] = &english.Entry{Text: w.Text, Weight: esdbRankedWeight(w.Weight)}
		general[w.Text] = true
	}
	for _, w := range v.technical {
		if e := entries[w.Text]; e == nil {
			entries[w.Text] = &english.Entry{Text: w.Text, Weight: rankedTechnical}
		} else {
			e.Weight = max(e.Weight, rankedTechnical)
		}
	}
	for _, text := range v.blocked {
		entries[text].NoSuggest = true
	}
	for key, s := range stats {
		e := entries[key]
		if e == nil {
			// 格式 1（ESDB＋technical.tsv）沒有的詞：短的在混打不認（english.Dictionary.Mixed，#358）。
			e = &english.Entry{Text: key, CorpusOnly: true}
			entries[key] = e
		}
		e.Weight = rankedWeight(s.perMillion)
		e.Protected = s.protected
		e.Display = displayForm(key, s.variants, general[key])
	}
	for key, form := range v.technicalDisplay {
		if e := entries[key]; e != nil {
			e.Display = form // 詞表手寫的寫法（Monday、PDF、AirPods）優先於語料
		}
	}
	keys := make([]string, 0, len(display))
	for key := range display {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		e := entries[key]
		if e == nil {
			return nil, nil, fmt.Errorf("display.tsv 的 %q 不在英文字典裡：這個檔只補寫法，不加詞", key)
		}
		e.Display = display[key] // 手寫的寫法優先於語料，同 technical.tsv 第三欄
	}
	list := make([]english.Entry, 0, len(entries))
	for _, e := range entries {
		list = append(list, *e)
	}
	d, err := english.NewRanked(list)
	if err != nil {
		return nil, nil, err
	}
	// 詞對只留兩端都在字典裡的英文詞（含撇號）。
	keep := func(pairs []english.Pair, left, right bool) []english.Pair {
		var out []english.Pair
		for _, p := range pairs {
			if (!left || d.Has(p.Left)) && (!right || d.Has(p.Right)) {
				out = append(out, p)
			}
		}
		return out
	}
	b, err := english.NewBigrams([3][]english.Pair{
		english.EnglishEnglish: keep(t.englishPairs, true, true),
		english.ChineseEnglish: keep(t.zhPair, false, true),
		english.EnglishChinese: keep(t.enZhPairs, true, false),
	})
	return d, b, err
}

// displayForm 是語料裡最常見的寫法，跟小寫一樣或不可靠時是空字串（#358，手機與桌面共用）：
//   - 語料分不出句首、標題的大寫：只有第一個字母大寫的寫法（Total、Images、Apple、Taiwan）不用；
//     ESDB 的一般英文詞（general）也不用語料的寫法（標題全大寫的 US、LINE、PLAYERS）。
//   - 留下的是 ESDB 以外、第一個字母以外也有大寫的寫法（AI、iPhone、OpenAI、XD），要過半（PO 不到五成，
//     照小寫）；以及代名詞 I 與 I 開頭的縮寫（I'm、I'll），要七成以上。單一字母只有 I 大寫。
//   - technical.tsv 第三欄與 display.tsv 手寫的寫法（Monday、PDF、Taiwan）另外蓋過這裡（buildRanked）。
func displayForm(key string, variants map[string]float64, general bool) string {
	var total, best float64
	top := ""
	for form, n := range variants {
		total += n
		if n > best || n == best && form < top {
			top, best = form, n
		}
	}
	if top == "" || top == key || len(key) == 1 && key != "i" {
		return ""
	}
	title := top == strings.ToUpper(key[:1])+key[1:]
	pronoun := key == "i" || strings.HasPrefix(key, "i'")
	if !pronoun && (general || title) {
		return ""
	}
	need := rankedOtherShare
	if len(key) > 1 && title {
		need = rankedTitleShare
	}
	if best < need*total {
		return ""
	}
	return top
}
