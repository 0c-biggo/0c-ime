package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/importer"
)

func TestStockNamesMergeAsCurated(t *testing.T) {
	for _, apply := range []string{"", "twse", "all"} {
		t.Run("apply="+apply, func(t *testing.T) {
			data, staging := t.TempDir(), t.TempDir()
			writeFile := func(root, name, content string) {
				t.Helper()
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			original := "甲乙\tㄐㄧㄚˇ ㄧˇ\tattested\ty\tcurated\t人工讀音\n" +
				"乙丙\tㄧˇ ㄅㄧㄥˇ\tprimary\ty\tcurated\tname-source:twse\n" +
				"庚辛\tㄍㄥ ㄒㄧㄣ\tcommon\ty\tcurated,mcbopomofo\t原有備註\n"
			writeFile(data, "lexicon/words.tsv", original)
			writeFile(data, "lexicon/chars.tsv", "# 空字表\n")
			writeFile(data, "lexicon/rejected.tsv", "丁戊\tㄉㄧㄥ ㄨˋ\t測試不收名稱\n")
			writeFile(data, "sources/ledger.tsv", "curated\t0c\thttps://example.org\tv1\tCC-BY-SA-4.0\tyes\t0c contributors\n"+
				"mcbopomofo\t小麥注音\thttps://example.org\tv1\tMIT\tyes\tcontributors\n")
			writeFile(data, "lm/unigram.tsv", "# 詞\tmcbopomofo\n甲乙\t1\n")
			writeFile(staging, "words-twse.tsv", "甲乙\tㄐㄧㄚˇ ㄧˇ\tprimary\ty\ttwse\n"+
				"丙丁\tㄅㄧㄥˇ ㄉㄧㄥ\tprimary\ty\ttwse\n"+
				"丁戊\tㄉㄧㄥ ㄨˋ\tprimary\ty\ttwse\n"+
				"庚辛\tㄍㄥ ㄒㄧㄣ\tprimary\ty\ttwse\n")
			merge(data, staging, apply)
			p := filepath.Join(data, "lexicon/words.tsv")
			read := func() string {
				t.Helper()
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			if apply == "" {
				if read() != original {
					t.Fatal("只報告不應寫回")
				}
				return
			}
			for _, line := range strings.Split(strings.TrimSpace(original), "\n") {
				if !strings.Contains(read(), line+"\n") {
					t.Fatalf("既有詞條遭更動或刪除：%s", line)
				}
			}
			table, err := importer.ReadTable(p, importer.Words)
			if err != nil {
				t.Fatal(err)
			}
			if len(table.Rows) != 4 || table.Origins()["twse"] {
				t.Fatalf("不應加入不收名稱或 twse 分類：%v", table.Rows)
			}
			if !strings.Contains(read(), "丙丁\tㄅㄧㄥˇ ㄉㄧㄥ\tprimary\ty\tcurated\tname-source:twse\n") {
				t.Fatal("新名稱未歸入 curated 或缺少來源追蹤")
			}
			first := read()
			merge(data, staging, apply)
			if read() != first {
				t.Fatal("重複套用應保持既有詞條與備註")
			}
		})
	}
}
