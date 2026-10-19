package importer

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/compile"
)

// Kind 是主詞庫檔的種類：決定哪幾欄構成一筆的鍵、等級與來源在哪一欄。
type Kind struct {
	Name   string // 報告用：「詞」或「字」
	Key    []int  // 構成鍵的欄
	Grade  int
	Origin int
	Cols   int // 必要欄數；之後是選填的備註欄
	staged func(*compile.Staging) [][]string
}

var (
	// Words 是 data/lexicon/words.tsv：詞面、讀音、grade、norm、origin、備註。
	Words = Kind{Name: "詞", Key: []int{0, 1}, Grade: 2, Origin: 4, Cols: 5, staged: func(st *compile.Staging) [][]string {
		var out [][]string
		for _, w := range st.Words {
			out = append(out, []string{w.Word, zhuyin.Key(w.Reading), w.Grade.String(), yn(w.Norm), strings.Join(w.Origins, ",")})
		}
		return out
	}}
	// Chars 是 data/lexicon/chars.tsv：字、讀音、grade、norm、variant_of、origin、備註。
	Chars = Kind{Name: "字", Key: []int{0, 1, 4}, Grade: 2, Origin: 5, Cols: 6, staged: func(st *compile.Staging) [][]string {
		var out [][]string
		for _, c := range st.Chars {
			out = append(out, []string{c.Char, string(c.Reading), c.Grade.String(), yn(c.Norm), string(c.VariantOf), strings.Join(c.Origins, ",")})
		}
		return out
	}}
)

func yn(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

func (k Kind) key(f []string) string {
	var parts []string
	for _, i := range k.Key {
		parts = append(parts, f[i])
	}
	return strings.Join(parts, "\t")
}

// Table 是一份主詞庫檔：開頭的註解行原樣保留，備註欄跟著所在的那一行。資料行依第一欄（詞面或字）
// 排序寫回；同一詞面或字的各行維持原本的先後，新的一行接在後面——同一字的讀音順序會影響
// 編譯時的讀音展開，排序不能打亂它。
type Table struct {
	Kind   Kind
	Header []string
	Rows   map[string][]string
	order  []string
}

// ReadTable 讀主詞庫檔。格式錯誤由 0c-compile 檢查，這裡只要求欄數足夠、鍵不重複。
func ReadTable(p string, k Kind) (*Table, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	t := &Table{Kind: k, Rows: map[string][]string{}}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if len(t.Rows) > 0 {
				return nil, fmt.Errorf("%s:%d：註解只能放在檔案開頭（寫回時會依鍵重新排序），說明請寫在備註欄", p, ln)
			}
			t.Header = append(t.Header, line)
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < k.Cols {
			return nil, fmt.Errorf("%s:%d：至少需要 %d 欄", p, ln, k.Cols)
		}
		key := k.key(f)
		if _, dup := t.Rows[key]; dup {
			return nil, fmt.Errorf("%s:%d：%s 重複", p, ln, strings.ReplaceAll(key, "\t", " "))
		}
		t.Rows[key] = f
		t.order = append(t.order, key)
	}
	return t, sc.Err()
}

// Write 寫回：依第一欄穩定排序。
func (t *Table) Write(p string) error {
	var keys []string
	for _, k := range t.order {
		if _, ok := t.Rows[k]; ok {
			keys = append(keys, k)
		}
	}
	first := func(k string) string { s, _, _ := strings.Cut(k, "\t"); return s }
	sort.SliceStable(keys, func(i, j int) bool { return first(keys[i]) < first(keys[j]) })
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, h := range t.Header {
		fmt.Fprintln(w, h)
	}
	for _, k := range keys {
		fmt.Fprintln(w, strings.Join(t.Rows[k], "\t"))
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Change 是差異報告的一筆。
type Change struct {
	Origin, What string // What：added、joined、removed、grade、rejected、nonbmp
	Row          []string
	Note         string
}

// Diff 比較一個來源的匯入暫存與主詞庫：
//   - added：主詞庫沒有、要新增的字詞讀音
//   - joined：主詞庫已有（來自其他來源或人工），要把這個來源加進 origin
//   - removed：主詞庫記著這個來源，但新版來源已經沒有；只剩這個來源時整行刪除
//   - grade：兩邊都有、來源給的等級比主詞庫高（主詞庫是各來源取最高；套用時調高）
//   - rejected：rejected.tsv 列為不收，不會加回來
//   - nonbmp：含基本平面以外的字，不自動新增，要人工審過再寫進主詞庫
func Diff(t *Table, staged [][]string, origin string, rejected map[string]bool) []Change {
	k := t.Kind
	stagedRows := map[string][]string{}
	for _, f := range staged {
		if !hasOrigin(f[k.Origin], origin) {
			continue
		}
		key := k.key(f)
		if old, ok := stagedRows[key]; ok && gradeOf(old[k.Grade]).PRW() >= gradeOf(f[k.Grade]).PRW() {
			continue
		}
		g := append([]string(nil), f...)
		g[k.Origin] = origin
		stagedRows[key] = g
	}
	var out []Change
	keys := make([]string, 0, len(stagedRows))
	for key := range stagedRows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		f := stagedRows[key]
		row, ok := t.Rows[key]
		switch {
		case !ok && rejected[f[0]+"\t"+f[1]]:
			out = append(out, Change{origin, "rejected", f, ""})
		case !ok && !bmp(f[0]):
			out = append(out, Change{origin, "nonbmp", f, ""})
		case !ok:
			out = append(out, Change{origin, "added", f, ""})
		case !hasOrigin(row[k.Origin], origin):
			out = append(out, Change{origin, "joined", row, ""})
		case gradeOf(f[k.Grade]).PRW() > gradeOf(row[k.Grade]).PRW():
			out = append(out, Change{origin, "grade", row, f[k.Grade]})
		}
	}
	var gone []string
	for key, row := range t.Rows {
		if hasOrigin(row[k.Origin], origin) {
			if _, ok := stagedRows[key]; !ok {
				gone = append(gone, key)
			}
		}
	}
	sort.Strings(gone)
	for _, key := range gone {
		out = append(out, Change{origin, "removed", t.Rows[key], ""})
	}
	for i := range out {
		if out[i].What == "joined" || out[i].What == "grade" {
			if f := stagedRows[k.key(out[i].Row)]; f != nil {
				out[i].Note = f[k.Grade]
			}
		}
	}
	return out
}

// Apply 把 Diff 的結果寫進表格（rejected、nonbmp 不動）。
func (t *Table) Apply(changes []Change) {
	k := t.Kind
	for _, c := range changes {
		key := k.key(c.Row)
		switch c.What {
		case "added":
			t.Rows[key] = append([]string(nil), c.Row[:k.Cols]...)
			t.order = append(t.order, key)
		case "joined", "grade":
			row := t.Rows[key]
			if c.What == "joined" {
				row[k.Origin] = addOrigin(row[k.Origin], c.Origin)
			}
			if gradeOf(c.Note).PRW() > gradeOf(row[k.Grade]).PRW() {
				row[k.Grade] = c.Note
			}
		case "removed":
			row := t.Rows[key]
			if o := removeOrigin(row[k.Origin], c.Origin); o == "" {
				delete(t.Rows, key)
			} else {
				row[k.Origin] = o
			}
		}
	}
}

func gradeOf(s string) compile.Grade {
	for _, g := range []compile.Grade{compile.Primary, compile.Common, compile.Correction, compile.Attested} {
		if g.String() == s {
			return g
		}
	}
	return compile.Attested
}

func bmp(s string) bool {
	for _, r := range s {
		if r > 0xFFFF {
			return false
		}
	}
	return true
}

func splitOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func hasOrigin(s, o string) bool {
	for _, x := range splitOrigins(s) {
		if x == o {
			return true
		}
	}
	return false
}

func addOrigin(s, o string) string {
	os := append(splitOrigins(s), o)
	sort.Strings(os)
	return strings.Join(os, ",")
}

func removeOrigin(s, o string) string {
	var out []string
	for _, x := range splitOrigins(s) {
		if x != o {
			out = append(out, x)
		}
	}
	return strings.Join(out, ",")
}

// StagedRows 把匯入暫存轉成主詞庫檔的欄位格式。
func (k Kind) StagedRows(st *compile.Staging) [][]string { return k.staged(st) }

// Origins 回傳表格裡出現過的來源 ID。
func (t *Table) Origins() map[string]bool {
	out := map[string]bool{}
	for _, row := range t.Rows {
		for _, o := range splitOrigins(row[t.Kind.Origin]) {
			out[o] = true
		}
	}
	return out
}
