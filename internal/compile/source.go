// Package compile 是詞庫編譯器：讀取 data/ 下的來源格式，展開讀音變體、驗證、
// 輸出執行期資料包（docs/13、docs/19）。
package compile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"git.biggo.com/0c/ime/core/zhuyin"
)

// Grade 是讀音等級（docs/13）。
type Grade int

const (
	Attested   Grade = iota // 可考：罕用或僅見於辭典
	Correction              // 自動修正用的錯誤讀音
	Common                  // 通行：常見，但不是最主要的打法
	Primary                 // 主讀音：台灣最通行的打法，不一定是規範音
)

var gradeNames = map[string]Grade{"attested": Attested, "correction": Correction, "common": Common, "primary": Primary}

func (g Grade) String() string {
	for k, v := range gradeNames {
		if v == g {
			return k
		}
	}
	return "?"
}

// PRW 是各等級的預設 P(r|w)。初值，待以資料校準（docs/13 未決事項 3）。
func (g Grade) PRW() float64 {
	return map[Grade]float64{Primary: 1, Common: 0.5, Correction: 0.3, Attested: 0.1}[g]
}

// Source 是來源帳本的一筆。
type Source struct {
	ID, Name, URL, Version, License string
	Distributable                   bool
	Attribution                     string
}

// CharReading 是 chars 檔的一筆。
type CharReading struct {
	Char      string
	Reading   zhuyin.Syllable
	Grade     Grade
	Norm      bool
	VariantOf zhuyin.Syllable // 非空：這是同一語素在 VariantOf 讀音下的變體，繼承到所有詞（L2）
	Origins   []string
}

// WordReading 是 words 檔的一筆。
type WordReading struct {
	Word    string
	Reading []zhuyin.Syllable
	Grade   Grade
	Norm    bool
	Origins []string
}

// Correction 是自動修正清單的一筆。
type CorrectionEntry struct {
	Word    string
	Reading []zhuyin.Syllable
	Origins []string
	Note    string
}

// Reach 是可達性回歸清單的一筆：打這串讀音，候選必須包含這個詞。
type Reach struct {
	Reading []zhuyin.Syllable
	Word    string
	Note    string
}

// Rejection 是 rejected.tsv 的一筆：審查過、不收的字詞讀音。它不在主詞庫裡；
// 0c-import 套用上游更新時據此不再把它加回來。
type Rejection struct {
	Word    string
	Reading []zhuyin.Syllable
	Note    string
	Loc     string
}

// Data 是 data/ 目錄讀進來的全部來源。
type Data struct {
	Sources     map[string]Source
	Chars       []CharReading
	Words       []WordReading
	Corrections []CorrectionEntry
	Reach       []Reach
	Rejected    []Rejection
	Counts      map[string]map[string]float64 // 來源 ID → 詞 → 次數（lm/unigram.tsv 的一欄）
	Bigrams     map[string]string             // 來源 ID → bigram 檔路徑（data/lm/bigram/<來源 ID>.tsv）
}

// Problem 是驗證錯誤；Line 為來源位置。
type Problem struct {
	Line, Msg string
}

func (p Problem) Error() string { return p.Line + "：" + p.Msg }

type lineReader struct {
	path    string
	strict  bool // 詞庫檔：任何問題都是錯誤；匯入暫存檔：略過並計數
	dropped *Dropped
	errs    *[]Problem
}

// Dropped 記錄匯入暫存檔因格式或音節不合法而略過的行：檔名 → 行數與前幾行例子。
type Dropped struct {
	Count   map[string]int
	Samples map[string][]string
}

func (lr lineReader) each(minCols int, fn func(loc string, f []string) error) error {
	fh, err := os.Open(lr.path)
	if err != nil {
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	name := filepath.Base(lr.path)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		loc := fmt.Sprintf("%s:%d", lr.path, ln)
		f := strings.Split(line, "\t")
		var perr error
		if len(f) < minCols {
			perr = fmt.Errorf("至少需要 %d 欄", minCols)
		} else {
			perr = fn(loc, f)
		}
		if perr != nil {
			if lr.strict {
				*lr.errs = append(*lr.errs, Problem{loc, perr.Error()})
			} else {
				lr.dropped.Count[name]++
				if len(lr.dropped.Samples[name]) < 5 {
					lr.dropped.Samples[name] = append(lr.dropped.Samples[name], line+"  ← "+perr.Error())
				}
			}
		}
	}
	return sc.Err()
}

func col(f []string, i int) string {
	if i < len(f) {
		return strings.TrimSpace(f[i])
	}
	return ""
}

func parseReading(s string, word string) ([]zhuyin.Syllable, error) {
	r := zhuyin.ParseReading(s)
	if len(r) == 0 {
		return nil, fmt.Errorf("缺少讀音")
	}
	for _, x := range r {
		if !x.Legal() {
			return nil, fmt.Errorf("音節 %q 不在國語音節表內", x)
		}
	}
	if word != "" && len([]rune(word)) != len(r) {
		return nil, fmt.Errorf("%q 有 %d 字、讀音有 %d 個音節", word, len([]rune(word)), len(r))
	}
	return r, nil
}

func parseGrade(s string) (Grade, error) {
	g, ok := gradeNames[s]
	if !ok {
		return 0, fmt.Errorf("不認得的等級 %q", s)
	}
	return g, nil
}

func parseOrigins(s string) ([]string, error) {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("缺少來源")
	}
	return out, nil
}

// charFile 讀一份字表（字、讀音、grade、norm、variant_of、origin）。
func charFile(lr lineReader, out *[]CharReading) error {
	return lr.each(6, func(loc string, f []string) error {
		if n := len([]rune(col(f, 0))); n != 1 {
			return fmt.Errorf("字欄須為單一字元")
		}
		r, err := parseReading(col(f, 1), col(f, 0))
		if err != nil {
			return err
		}
		g, err := parseGrade(col(f, 2))
		if err != nil {
			return err
		}
		var vof zhuyin.Syllable
		if v := col(f, 4); v != "" {
			vr, err := parseReading(v, "")
			if err != nil || len(vr) != 1 {
				return fmt.Errorf("variant_of 須為單一合法音節")
			}
			vof = vr[0]
		}
		o, err := parseOrigins(col(f, 5))
		if err != nil {
			return err
		}
		*out = append(*out, CharReading{col(f, 0), r[0], g, col(f, 3) == "y", vof, o})
		return nil
	})
}

// wordFile 讀一份詞表（詞面、讀音、grade、norm、origin）。
func wordFile(lr lineReader, out *[]WordReading) error {
	return lr.each(5, func(loc string, f []string) error {
		r, err := parseReading(col(f, 1), col(f, 0))
		if err != nil {
			return err
		}
		g, err := parseGrade(col(f, 2))
		if err != nil {
			return err
		}
		if g == Correction {
			return fmt.Errorf("修正讀音請寫在 corrections.tsv")
		}
		o, err := parseOrigins(col(f, 4))
		if err != nil {
			return err
		}
		*out = append(*out, WordReading{col(f, 0), r, g, col(f, 3) == "y", o})
		return nil
	})
}

// Load 讀取 data/ 目錄。詞庫只有一份：lexicon/chars.tsv 與 words.tsv 裡每個字詞讀音一行，
// 來源寫在 origin 欄；任何問題都是錯誤。外部來源的更新由 0c-import 審過再寫進這兩個檔。
func Load(dir string) (*Data, []Problem, error) {
	d := &Data{Sources: map[string]Source{}, Counts: map[string]map[string]float64{}, Bigrams: map[string]string{}}
	var errs []Problem
	strict := func(p string) lineReader { return lineReader{path: p, strict: true, errs: &errs} }

	if err := strict(filepath.Join(dir, "sources", "ledger.tsv")).each(6, func(loc string, f []string) error {
		dist := col(f, 5)
		if dist != "yes" && dist != "no" {
			return fmt.Errorf("可散布欄須為 yes 或 no")
		}
		s := Source{ID: col(f, 0), Name: col(f, 1), URL: col(f, 2), Version: col(f, 3), License: col(f, 4), Distributable: dist == "yes", Attribution: col(f, 6)}
		if _, dup := d.Sources[s.ID]; dup {
			return fmt.Errorf("來源 ID %q 重複", s.ID)
		}
		d.Sources[s.ID] = s
		return nil
	}); err != nil {
		return nil, nil, err
	}

	lex := filepath.Join(dir, "lexicon")
	if p := filepath.Join(lex, "chars.tsv"); exists(p) {
		if err := charFile(strict(p), &d.Chars); err != nil {
			return nil, nil, err
		}
	}
	if p := filepath.Join(lex, "words.tsv"); exists(p) {
		if err := wordFile(strict(p), &d.Words); err != nil {
			return nil, nil, err
		}
	}
	errs = append(errs, duplicates(d)...)
	if p := filepath.Join(lex, "rejected.tsv"); exists(p) {
		if err := strict(p).each(3, func(loc string, f []string) error {
			r, err := parseReading(col(f, 1), col(f, 0))
			if err != nil {
				return err
			}
			if col(f, 2) == "" {
				return fmt.Errorf("請寫明不收的依據")
			}
			d.Rejected = append(d.Rejected, Rejection{Word: col(f, 0), Reading: r, Note: col(f, 2), Loc: loc})
			return nil
		}); err != nil {
			return nil, nil, err
		}
	}
	errs = append(errs, d.checkRejected()...)
	if p := filepath.Join(lex, "corrections.tsv"); exists(p) {
		if err := strict(p).each(3, func(loc string, f []string) error {
			r, err := parseReading(col(f, 1), col(f, 0))
			if err != nil {
				return err
			}
			o, err := parseOrigins(col(f, 2))
			if err != nil {
				return err
			}
			d.Corrections = append(d.Corrections, CorrectionEntry{col(f, 0), r, o, col(f, 3)})
			return nil
		}); err != nil {
			return nil, nil, err
		}
	}
	if p := filepath.Join(lex, "reachability.tsv"); exists(p) {
		if err := strict(p).each(2, func(loc string, f []string) error {
			r, err := parseReading(col(f, 0), col(f, 1))
			if err != nil {
				return err
			}
			d.Reach = append(d.Reach, Reach{r, col(f, 1), col(f, 2)})
			return nil
		}); err != nil {
			return nil, nil, err
		}
	}

	if p := filepath.Join(dir, "lm", "unigram.tsv"); exists(p) {
		u, err := ReadUnigram(p)
		if err != nil {
			errs = append(errs, Problem{p, err.Error()})
		} else {
			for i, src := range u.Sources {
				m := map[string]float64{}
				for _, w := range u.Words {
					if v := u.Cells[w][i]; v != "" {
						n, err := strconv.ParseFloat(v, 64)
						if err != nil || n < 0 {
							errs = append(errs, Problem{p, fmt.Sprintf("%s 的 %s 次數 %q 不合法", w, src, v)})
							continue
						}
						m[w] += n
					}
				}
				d.Counts[src] = m
			}
		}
	}
	// bigram：data/lm/bigram/<來源 ID>.tsv，「前詞<TAB>詞<TAB>P(詞|前詞)」。檔案很大，只記路徑，編譯時才讀。
	bigrams, _ := filepath.Glob(filepath.Join(dir, "lm", "bigram", "*.tsv"))
	for _, p := range bigrams {
		d.Bigrams[strings.TrimSuffix(filepath.Base(p), ".tsv")] = p
	}
	return d, errs, nil
}

// duplicates：主詞庫裡同一個字詞讀音只能有一行，來源合併寫在 origin 欄。
func duplicates(d *Data) []Problem {
	var errs []Problem
	seen := map[string]bool{}
	for _, c := range d.Chars {
		k := c.Char + "\t" + string(c.Reading) + "\t" + string(c.VariantOf)
		if seen[k] {
			errs = append(errs, Problem{"lexicon/chars.tsv", fmt.Sprintf("%s %s 重複出現，同一字的同一讀音只能有一行", c.Char, c.Reading)})
		}
		seen[k] = true
	}
	for _, w := range d.Words {
		k := w.Word + "\t" + zhuyin.Key(w.Reading)
		if seen[k] {
			errs = append(errs, Problem{"lexicon/words.tsv", fmt.Sprintf("%s %s 重複出現，同一詞的同一讀音只能有一行", w.Word, zhuyin.Key(w.Reading))})
		}
		seen[k] = true
	}
	return errs
}

// checkRejected：rejected.tsv 的讀音不能同時在詞庫裡，也不能重複列。
func (d *Data) checkRejected() []Problem {
	var errs []Problem
	type key struct{ word, reading string }
	in := map[key]bool{}
	for _, c := range d.Chars {
		in[key{c.Char, string(c.Reading)}] = true
	}
	for _, w := range d.Words {
		in[key{w.Word, zhuyin.Key(w.Reading)}] = true
	}
	seen := map[key]bool{}
	for _, x := range d.Rejected {
		k := key{x.Word, zhuyin.Key(x.Reading)}
		if seen[k] {
			errs = append(errs, Problem{x.Loc, fmt.Sprintf("%s %s 重複列出", x.Word, k.reading)})
		}
		seen[k] = true
		if in[k] {
			errs = append(errs, Problem{x.Loc, fmt.Sprintf("%s %s 列為不收，卻仍在詞庫裡", x.Word, k.reading)})
		}
	}
	return errs
}

// Staging 是 0c-import 從外部來源產生、尚未套進詞庫的字詞讀音（寬鬆讀取，不合法的行略過並計數）。
type Staging struct {
	Chars   []CharReading
	Words   []WordReading
	Dropped Dropped
}

// LoadStaging 讀匯入暫存目錄裡的 chars-*.tsv 與 words-*.tsv。
func LoadStaging(dir string) (*Staging, error) {
	st := &Staging{Dropped: Dropped{Count: map[string]int{}, Samples: map[string][]string{}}}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tsv"))
	sort.Strings(files)
	for _, p := range files {
		lr := lineReader{path: p, dropped: &st.Dropped}
		var err error
		switch base := filepath.Base(p); {
		case strings.HasPrefix(base, "chars-"):
			err = charFile(lr, &st.Chars)
		case strings.HasPrefix(base, "words-"):
			err = wordFile(lr, &st.Words)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return st, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
