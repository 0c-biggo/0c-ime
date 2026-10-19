// 0c-audit 列出讀音整理的優先清單。只報告，不自動刪詞或指定主讀音。
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/compile"
)

type finding struct {
	word, kind, readings, origins string
	count                         float64
}

func findings(d *compile.Data, excluded string, bmpOnly bool, source string, minCount float64) []finding {
	exclude := map[string]bool{}
	for _, id := range strings.Split(excluded, ",") {
		exclude[strings.TrimSpace(id)] = true
	}
	byChar := map[string][]compile.CharReading{}
	for _, c := range d.Chars {
		if bmpOnly && []rune(c.Char)[0] > 0xffff {
			continue
		}
		keep := false
		for _, id := range c.Origins {
			keep = keep || !exclude[id]
		}
		if keep {
			byChar[c.Char] = append(byChar[c.Char], c)
		}
	}
	var out []finding
	for word, rows := range byChar {
		count := d.Counts[source][word]
		if count < minCount {
			continue
		}
		grades := map[string]compile.Grade{}
		primary, origins := map[string]bool{}, map[string]bool{}
		for _, r := range rows {
			y := string(r.Reading)
			if g, ok := grades[y]; !ok || r.Grade > g {
				grades[y] = r.Grade
			}
			if r.Grade == compile.Primary && r.VariantOf == "" {
				primary[y] = true
			}
			for _, id := range r.Origins {
				origins[id] = true
			}
		}
		kind := ""
		if len(primary) == 0 {
			kind = "missing-primary"
		}
		if len(primary) > 1 {
			kind = "multiple-primary"
		}
		if kind == "" {
			continue
		}
		var readings, ids []string
		for r, grade := range grades {
			readings = append(readings, r+":"+grade.String())
		}
		for id := range origins {
			ids = append(ids, id)
		}
		sort.Strings(readings)
		sort.Strings(ids)
		out = append(out, finding{word, kind, strings.Join(readings, ";"), strings.Join(ids, ","), count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].word < out[j].word
	})
	return out
}

// 詞頻不能推導讀音比例；這裡只找來源沒有主音依據的詞，供相對權重與人工稽核。
func wordFindings(d *compile.Data, excluded string, bmpOnly bool, source string, minCount float64) []finding {
	exclude := map[string]bool{}
	for _, id := range strings.Split(excluded, ",") {
		exclude[strings.TrimSpace(id)] = true
	}
	type row struct {
		grades  map[string]compile.Grade
		origins map[string]bool
	}
	words := map[string]*row{}
	for _, w := range d.Words {
		if len([]rune(w.Word)) < 2 || d.Counts[source][w.Word] < minCount {
			continue
		}
		keep := false
		for _, id := range w.Origins {
			keep = keep || !exclude[id]
		}
		if bmpOnly {
			for _, r := range w.Word {
				if r > 0xffff {
					keep = false
				}
			}
		}
		if !keep {
			continue
		}
		x := words[w.Word]
		if x == nil {
			x = &row{map[string]compile.Grade{}, map[string]bool{}}
			words[w.Word] = x
		}
		y := zhuyin.Key(w.Reading)
		if g, ok := x.grades[y]; !ok || w.Grade > g {
			x.grades[y] = w.Grade
		}
		for _, id := range w.Origins {
			x.origins[id] = true
		}
	}
	var out []finding
	for word, x := range words {
		primary := 0
		var readings, origins []string
		for r, g := range x.grades {
			if g == compile.Primary {
				primary++
			}
			readings = append(readings, r+":"+g.String())
		}
		kind := ""
		if primary == 0 {
			kind = "word-missing-primary"
		}
		if primary > 1 {
			kind = "word-multiple-primary"
		}
		if kind == "" {
			continue
		}
		for id := range x.origins {
			origins = append(origins, id)
		}
		sort.Strings(readings)
		sort.Strings(origins)
		out = append(out, finding{word, kind, strings.Join(readings, ";"), strings.Join(origins, ","), d.Counts[source][word]})
	}
	return out
}

func main() {
	dir := flag.String("data", "data", "來源資料目錄")
	exclude := flag.String("exclude", "", "排除的來源 ID，以逗號分隔")
	bmp := flag.Bool("bmp-only", true, "僅檢查基本平面字元（同預設編譯配置）")
	counts := flag.String("counts", "mcbopomofo", "用來安排審查優先次序的單字詞頻來源")
	minimum := flag.Float64("min-count", 100, "單字詞頻下限；0 包含低頻字")
	limit := flag.Int("limit", 100, "最多輸出幾個字；0 全部")
	flag.Parse()
	d, errs, err := compile.Load(*dir)
	if err != nil || len(errs) > 0 {
		fmt.Fprintln(os.Stderr, err, errs)
		os.Exit(1)
	}
	if _, ok := d.Counts[*counts]; !ok {
		fmt.Fprintln(os.Stderr, "沒有詞頻來源：", *counts)
		os.Exit(1)
	}
	rows := findings(d, *exclude, *bmp, *counts, *minimum)
	rows = append(rows, wordFindings(d, *exclude, *bmp, *counts, *minimum)...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		if rows[i].word != rows[j].word {
			return rows[i].word < rows[j].word
		}
		return rows[i].kind < rows[j].kind
	})
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}
	w := csv.NewWriter(os.Stdout)
	w.Comma = '\t'
	w.Write([]string{"issue", "word", "count", "readings", "origins"})
	for _, r := range rows {
		w.Write([]string{r.kind, r.word, fmt.Sprintf("%.0f", r.count), r.readings, r.origins})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
