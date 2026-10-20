// 0c-reading-eval 以人工編審的辭典讀音量詞庫的讀音品質（docs/49 第 4 步）：辭典裡的每個多字詞，
// 詞庫有沒有收、照辭典的讀音打不打得出來、打出來是不是首選。CV 等句子評測的參考讀音由詞庫
// 自動標注，量不到讀音本身的對錯，這個評測補上那一塊。辭典檔只在 data/raw 讀取，不進 repo。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

func main() {
	data := flag.String("data", "build/data", "資料包目錄")
	dict := flag.String("dict", "data/raw/libchewing-data/dict/moe/moe_dict_concised.csv", "參考辭典（每行：詞,旗標,讀音；libchewing-data 的教育部國語辭典簡編本）")
	errPath := flag.String("errors", "", "把打不出來與不是首選的詞寫到這個檔")
	first := flag.Bool("first", true, "也量首選（逐詞打字，較慢）")
	flag.Parse()

	eng, err := datadir.Open(*data)
	check(err)
	lex, ok := eng.Model.Lex.(*lexicon.Compact)
	if !ok {
		check(fmt.Errorf("需要編譯後的資料包"))
	}
	ty := eval.NewTypist(eng)

	type item struct {
		word    string
		reading []zhuyin.Syllable
	}
	var items []item
	seen := map[string]bool{}
	f, err := os.Open(*dict)
	check(err)
	sc := bufio.NewScanner(f)
	skipped := 0
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "#") || l == "" {
			continue
		}
		p := strings.Split(l, ",")
		if len(p) < 3 {
			skipped++
			continue
		}
		w := strings.TrimSpace(p[0])
		r := zhuyin.ParseReading(strings.Join(strings.Fields(p[2]), " "))
		ok := len([]rune(w)) >= 2 && len(r) == len([]rune(w)) && isHan(w)
		for _, s := range r {
			ok = ok && s.Legal()
		}
		if !ok {
			skipped++
			continue
		}
		if k := w + "\t" + zhuyin.Key(r); !seen[k] {
			seen[k] = true
			items = append(items, item{w, r})
		}
	}
	check(sc.Err())
	f.Close()

	var total, inLex, reachable, firstOK int
	var misses []string
	for _, it := range items {
		total++
		if !lex.Has(it.word) {
			continue
		}
		inLex++
		hit := false
		for _, e := range lex.Exact(it.reading) {
			hit = hit || e.Surface == it.word
		}
		if !hit {
			misses = append(misses, fmt.Sprintf("打不出\t%s\t%s\t%s", it.word, zhuyin.Key(it.reading), readings(lex, it.word)))
			continue
		}
		reachable++
		if *first {
			for _, s := range it.reading {
				ty.Type(s)
			}
			if got := ty.Commit(); got == it.word {
				firstOK++
			} else {
				misses = append(misses, fmt.Sprintf("非首選\t%s\t%s\t%s", it.word, zhuyin.Key(it.reading), got))
			}
		}
	}
	pct := func(a, b int) float64 { return 100 * float64(a) / float64(max(1, b)) }
	fmt.Printf("參考辭典 %d 個多字詞讀音（略過 %d 行）；詞庫收錄 %d（%.1f%%）\n", total, skipped, inLex, pct(inLex, total))
	fmt.Printf("照辭典讀音打得出：%d／%d（%.2f%%）\n", reachable, inLex, pct(reachable, inLex))
	if *first {
		fmt.Printf("照辭典讀音打，首選就是這個詞：%d／%d（%.2f%%）\n", firstOK, inLex, pct(firstOK, inLex))
	}
	if *errPath != "" {
		sort.Strings(misses)
		check(os.WriteFile(*errPath, []byte(strings.Join(misses, "\n")+"\n"), 0o644))
	}
}

// readings 列出詞庫裡這個詞的讀音（含規則展開），供審查。
func readings(lex *lexicon.Compact, w string) string {
	var out []string
	for _, e := range lex.Lookup(w) {
		out = append(out, zhuyin.Key(e.Reading))
	}
	sort.Strings(out)
	return strings.Join(out, "／")
}

func isHan(s string) bool {
	for _, r := range s {
		if !eval.IsHan(r) {
			return false
		}
	}
	return true
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-reading-eval:", err)
		os.Exit(1)
	}
}
