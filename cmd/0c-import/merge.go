package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/compile"
	"github.com/0c-biggo/0c-ime/internal/importer"
)

var changeNames = []struct{ what, label string }{
	{"added", "新增"}, {"joined", "加上來源"}, {"removed", "來源已沒有"}, {"grade", "來源等級較高"},
	{"rejected", "列為不收"}, {"nonbmp", "基本平面外（不自動新增）"},
}

// merge 比對匯入暫存與主詞庫，把每個來源的差異寫到 <imp>/diff/<來源>.tsv；apply 指定的來源寫回 data/。
func merge(data, imp, apply string) string {
	st, err := compile.LoadStaging(imp)
	check(err)
	d, errs, err := compile.Load(data)
	check(err)
	if len(errs) > 0 {
		check(fmt.Errorf("主詞庫有錯誤，先修正再匯入：%v", errs))
	}
	rejected := map[string]bool{}
	for _, x := range d.Rejected {
		rejected[x.Word+"\t"+zhuyin.Key(x.Reading)] = true
	}
	wp, cp := filepath.Join(data, "lexicon", "words.tsv"), filepath.Join(data, "lexicon", "chars.tsv")
	wt, err := importer.ReadTable(wp, importer.Words)
	check(err)
	ct, err := importer.ReadTable(cp, importer.Chars)
	check(err)
	up := filepath.Join(data, "lm", "unigram.tsv")
	u, err := compile.ReadUnigram(up)
	check(err)

	stagedW, stagedC := importer.Words.StagedRows(st), importer.Chars.StagedRows(st)
	origins := map[string]bool{}
	for _, rows := range [][][]string{stagedW, stagedC} {
		for _, f := range rows {
			for _, o := range strings.Split(f[len(f)-1], ",") {
				origins[o] = true
			}
		}
	}
	counts := stagedCounts(imp)
	selected := map[string]bool{}
	for _, o := range strings.Split(apply, ",") {
		if o = strings.TrimSpace(o); o == "all" {
			for x := range wt.Origins() {
				selected[x] = true
			}
			for x := range ct.Origins() {
				selected[x] = true
			}
			for _, x := range u.Sources {
				selected[x] = true
			}
			delete(selected, "curated")
			// 股票名稱併入自有詞條；twse 只用來選取匯入批次，不再是詞庫來源分類。
			selected["twse"] = true
		} else if o != "" {
			selected[o] = true
		}
	}

	var b strings.Builder
	for _, name := range []string{"chars", "words"} {
		for f, n := range st.Dropped.Count {
			if strings.HasPrefix(f, name) {
				fmt.Fprintf(&b, "暫存 %s 略過 %d 行（格式或音節不合法），例：%s\n", f, n, strings.Join(st.Dropped.Samples[f], "；"))
			}
		}
	}
	check(os.MkdirAll(filepath.Join(imp, "diff"), 0o755))
	var ids []string
	for o := range origins {
		ids = append(ids, o)
	}
	sort.Strings(ids)
	changed := false
	for _, o := range ids {
		wc := importer.Diff(wt, stagedW, o, rejected)
		cc := importer.Diff(ct, stagedC, o, rejected)
		if o == "twse" {
			wc = stockNameChanges(wc)
			cc = nil
		}
		writeChanges(filepath.Join(imp, "diff", o+".tsv"), wc, cc)
		n := map[string]int{}
		for _, c := range append(wc, cc...) {
			n[c.What]++
		}
		var parts []string
		for _, c := range changeNames {
			if n[c.what] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", c.label, n[c.what]))
			}
		}
		state := "只報告"
		if selected[o] {
			wt.Apply(wc)
			ct.Apply(cc)
			if o == "twse" {
				for _, c := range wc {
					if c.What == "added" {
						key := c.Row[0] + "\t" + c.Row[1]
						wt.Rows[key] = append(wt.Rows[key], "name-source:twse")
					}
				}
			}
			changed, state = true, "已寫回"
		}
		if len(parts) == 0 {
			parts = []string{"沒有差異"}
		}
		fmt.Fprintf(&b, "%s（%s）：%s\n", o, state, strings.Join(parts, "、"))
	}
	var cs []string
	for o := range counts {
		cs = append(cs, o)
	}
	sort.Strings(cs)
	for _, o := range cs {
		if selected[o] {
			u.SetColumn(o, counts[o])
			changed = true
			fmt.Fprintf(&b, "詞頻 %s：%d 詞（已寫回 lm/unigram.tsv）\n", o, len(counts[o]))
		} else {
			fmt.Fprintf(&b, "詞頻 %s：%d 詞（只報告）\n", o, len(counts[o]))
		}
	}
	if changed {
		check(wt.Write(wp))
		check(ct.Write(cp))
		check(u.Write(up))
		b.WriteString("主詞庫已更新，請以 git diff 審查，並重新編譯與評測。\n")
	}
	fmt.Fprintf(&b, "差異明細：%s\n", filepath.Join(imp, "diff"))
	check(os.WriteFile(filepath.Join(imp, "diff.txt"), []byte(b.String()), 0o644))
	return b.String()
}

// stockNameChanges 將新股票名稱收為自有詞條，原始名稱清單的出處另存於備註。
// 已收錄的讀音與人工註記不隨清單變動而覆寫或刪除；未審過的新名稱仍走既有排除規則。
func stockNameChanges(changes []importer.Change) []importer.Change {
	var out []importer.Change
	for _, c := range changes {
		switch c.What {
		case "added", "rejected", "nonbmp":
			c.Origin = "curated"
			c.Row = append([]string(nil), c.Row...)
			c.Row[importer.Words.Origin] = "curated"
			out = append(out, c)
		}
	}
	return out
}

// stagedCounts 讀 <imp>/counts-<來源>.tsv（第一行 "# origin: <來源 ID>"）。
func stagedCounts(imp string) map[string]map[string]string {
	out := map[string]map[string]string{}
	files, _ := filepath.Glob(filepath.Join(imp, "counts-*.tsv"))
	for _, p := range files {
		f, err := os.Open(p)
		check(err)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		origin := ""
		m := map[string]string{}
		for ln := 0; sc.Scan(); ln++ {
			t := sc.Text()
			if ln == 0 {
				origin = strings.TrimSpace(strings.TrimPrefix(t, "# origin:"))
				continue
			}
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			w, n, ok := strings.Cut(t, "\t")
			if v, err := strconv.ParseFloat(strings.TrimSpace(n), 64); ok && err == nil && v >= 0 {
				m[strings.TrimSpace(w)] = strings.TrimSpace(n)
			}
		}
		f.Close()
		check(sc.Err())
		if origin != "" {
			out[origin] = m
		}
	}
	return out
}

func writeChanges(p string, groups ...[]importer.Change) {
	f, err := os.Create(p)
	check(err)
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "# 差異\t欄位（同主詞庫檔）\t備註（等級不同時為來源的等級）")
	for _, g := range groups {
		for _, c := range g {
			fmt.Fprintf(w, "%s\t%s\t%s\n", c.What, strings.Join(c.Row, "\t"), c.Note)
		}
	}
	check(w.Flush())
	check(f.Close())
}
