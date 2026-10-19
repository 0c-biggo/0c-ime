// 0c-import 把 data/raw/ 的外部來源轉成匯入暫存（先執行 data/pipeline/fetch.sh），和 data/ 的主詞庫比對產生
// 差異報告；-apply 指定的來源才寫回 data/lexicon/words.tsv、chars.tsv 與 data/lm/unigram.tsv。
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.biggo.com/0c/ime/internal/compile"
	"git.biggo.com/0c/ime/internal/eval"
	"git.biggo.com/0c/ime/internal/importer"
)

func main() {
	raw := flag.String("raw", "data/raw", "外部來源目錄")
	data := flag.String("data", "data", "主詞庫所在的資料目錄")
	out := flag.String("out", "build/import", "匯入暫存與差異報告的目錄")
	apply := flag.String("apply", "", "寫回主詞庫的來源 ID（逗號分隔；all＝主詞庫裡已有的匯入來源）；空白只產生差異報告")
	newWords := flag.Bool("cedict-new-words", true, "匯入 CC-CEDICT 中小麥注音沒有的詞（否則只取已知詞的其他讀音）")
	flag.Parse()
	imp := *out
	check(os.MkdirAll(imp, 0o755))

	// 小麥注音
	md := filepath.Join(*raw, "McBopomofo", "Source", "Data")
	var het [3]io.Reader
	for i := range het {
		het[i] = open(filepath.Join(md, fmt.Sprintf("heterophony%d.list", i+1)))
	}
	m, err := importer.ReadMcBopomofo(open(filepath.Join(md, "BPMFBase.txt")), open(filepath.Join(md, "BPMFMappings.txt")), open(filepath.Join(md, "phrase.occ")), het)
	check(err)
	var cl []fmt.Stringer
	for _, c := range m.Chars {
		cl = append(cl, c)
	}
	write(filepath.Join(imp, "chars-mcbopomofo.tsv"), "# 字\t讀音\tgrade\tnorm\tvariant_of\torigin（由 0c-import 自小麥注音 BPMFBase.txt 與破音字優先表產生）", cl)
	var wl []fmt.Stringer
	known := map[string]bool{}
	lex := map[string]bool{}
	for _, w := range m.Words {
		wl = append(wl, w)
		known[w.Word] = true
		lex[w.Word] = true
	}
	for _, c := range m.Chars {
		lex[c.Char] = true
	}
	write(filepath.Join(imp, "words-mcbopomofo.tsv"), "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自小麥注音 BPMFMappings.txt 產生）", wl)

	free := importer.FreeCounts(m.Occ, lex)
	var counts []fmt.Stringer
	var keys []string
	for w, n := range free {
		if n > 0 {
			keys = append(keys, w)
		}
	}
	sort.Strings(keys)
	for _, w := range keys {
		counts = append(counts, line(fmt.Sprintf("%s\t%g", w, free[w])))
	}
	write(filepath.Join(imp, "counts-mcbopomofo.tsv"), "# origin: mcbopomofo\n# 詞\t自由次數（phrase.occ 的子字串計數經由長到短扣除校正，見 importer.FreeCounts）", counts)

	// 全字庫
	cns := filepath.Join(*raw, "cns")
	maps, _ := filepath.Glob(filepath.Join(cns, "Unicode", "CNS2UNICODE_*.txt"))
	var mr []io.Reader
	for _, p := range maps {
		mr = append(mr, open(p))
	}
	mcb := map[string]bool{}
	for _, c := range m.Chars {
		mcb[c.Char+"\t"+c.Reading] = true
	}
	cs, placeholders, err := importer.ReadCNS(open(filepath.Join(cns, "CNS_phonetic.txt")), mr,
		func(ch, r string) bool { return mcb[ch+"\t"+r] })
	check(err)
	cl = cl[:0]
	for _, c := range cs {
		cl = append(cl, c)
	}
	write(filepath.Join(imp, "chars-cns11643.tsv"), "# 字\t讀音\tgrade\tnorm\tvariant_of\torigin（由 0c-import 自全字庫 CNS_phonetic.txt 產生）", cl)

	// CC-CEDICT
	gz, err := gzip.NewReader(open(filepath.Join(*raw, "cedict.txt.gz")))
	check(err)
	ws, skipped, err := importer.ReadCEDICT(gz, known)
	check(err)
	wl = wl[:0]
	kept := 0
	for _, w := range ws {
		if !*newWords && !known[w.Word] {
			continue
		}
		wl = append(wl, w)
		kept++
	}
	rules, tw := importer.TaiwanReadings(ws, m.Words)
	for _, w := range tw {
		if *newWords {
			wl = append(wl, w)
		}
	}
	write(filepath.Join(imp, "words-cc-cedict.tsv"), "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自 CC-CEDICT 產生；只有 CC-CEDICT 的詞另加依小麥注音學到的台灣讀音，見 importer.TaiwanReadings）", wl)

	// 新酷音 v4 語言模型（CC BY 4.0）：unigram 轉為虛擬次數；bigram 只留兩個詞都在詞庫內者
	lex = surfaces(*data, imp)
	cv4 := filepath.Join(*raw, "libchewing-data", "dict", "chewing_v4")
	var cvUni []fmt.Stringer
	cvKeys := []string{}
	cvP := map[string]float64{}
	check(importer.ReadARPA(open(filepath.Join(cv4, "tsi_unigram.arpa")), func(o int, lp float64, ws []string) {
		if o == 1 && importer.IsHan(ws[0]) {
			cvP[ws[0]] = math.Pow(10, lp)
			cvKeys = append(cvKeys, ws[0])
		}
	}))
	sort.Strings(cvKeys)
	for _, w := range cvKeys {
		cvUni = append(cvUni, line(fmt.Sprintf("%s\t%.4g", w, cvP[w]*1e8)))
	}
	write(filepath.Join(imp, "counts-chewing-v4.tsv"), "# origin: chewing-v4\n# 詞\t虛擬次數（tsi_unigram.arpa 的機率 × 1e8）", cvUni)
	check(os.MkdirAll(filepath.Join(*data, "lm", "bigram"), 0o755))
	bf, err := os.Create(filepath.Join(*data, "lm", "bigram", "chewing-v4.tsv"))
	check(err)
	bw := bufio.NewWriter(bf)
	fmt.Fprintln(bw, "# origin: chewing-v4\n# 前詞\t詞\tP(詞|前詞)（bigram_p50.arpa；只留兩個詞都在 Zuin 詞庫內者）")
	var biKept, biAll int
	check(importer.ReadARPA(open(filepath.Join(cv4, "bigram_p50.arpa")), func(o int, lp float64, ws []string) {
		if o != 2 {
			return
		}
		biAll++
		if lex[ws[0]] && lex[ws[1]] {
			fmt.Fprintf(bw, "%s\t%s\t%.5g\n", ws[0], ws[1], math.Pow(10, lp))
			biKept++
		}
	}))
	check(bw.Flush())
	check(bf.Close())

	// 內政部全國路名：路名與鄉鎮市區名。讀音以目前的詞庫斷詞標注（地名的破音字可能標錯）。
	roads := importPlaces(*data, filepath.Join(*raw, "moi", "roads-115.csv"), imp)
	stocks := importStocks(*data, filepath.Join(*raw, "twse"), imp)
	lists := importNameLists(*data, *raw, imp)

	fmt.Printf("小麥注音：單字讀音 %d、詞讀音 %d、有次數的詞 %d\n全字庫：單字讀音 %d（略過只有佔位讀音 ㄇㄡˇ 的字 %d）\nCC-CEDICT：詞讀音 %d（拼音無法轉換而略過 %d）；台灣讀音規則 %d 條，補 %d 個讀音\n",
		len(m.Chars), len(m.Words), len(keys), len(cs), placeholders, kept, skipped, rules, len(tw))
	fmt.Printf("新酷音 v4：unigram %d 詞；bigram 保留 %d／%d（兩個詞都在 Zuin 詞庫內）\n", len(cvKeys), biKept, biAll)
	fmt.Printf("內政部路名：%s\n", roads)
	fmt.Printf("證交所股票簡稱：%s\n", stocks)
	for _, l := range lists {
		fmt.Printf("名稱清單 %s\n", l)
	}

	report := merge(*data, imp, *apply)
	fmt.Print(report)
}

// importPlaces 讀取全國路名 CSV（city,site_id,road），寫出 words-moi-places.tsv。
func importPlaces(data, csvPath, imp string) string {
	f, err := os.Open(csvPath)
	if err != nil {
		return "略過（" + err.Error() + "）"
	}
	defer f.Close()
	rows, err := csv.NewReader(bufio.NewReader(f)).ReadAll()
	check(err)
	names := map[string]bool{}
	for i, r := range rows {
		if i == 0 || len(r) < 3 {
			continue
		}
		city := strings.TrimPrefix(r[0], "\ufeff")
		names[r[2]] = true
		names[strings.TrimPrefix(r[1], city)] = true // 鄉鎮市區
		names[city] = true
	}
	return annotateNames(data, imp, names, "moi-places", 8, "words-moi-places.tsv", "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自內政部全國路名資料產生；讀音以詞庫斷詞自動標注）")
}

// importStocks 讀證交所 ISIN 清單（data/pipeline/fetch.sh 下載並轉 UTF-8），寫出 words-twse.tsv。
// twse 是暫存批次名稱；merge 收為 curated，並以 name-source:twse 備註追蹤名稱出處。
func importStocks(data, dir, imp string) string {
	names := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(dir, "isin-*.html"))
	if len(files) == 0 {
		return "略過（沒有 " + filepath.Join(dir, "isin-*.html") + "）"
	}
	for _, p := range files {
		ns, err := importer.ReadTWSEISIN(open(p))
		check(err)
		for _, n := range ns {
			names[n] = true
		}
	}
	return annotateNames(data, imp, names, "twse", 6, "words-twse.tsv", "# 詞面\t讀音\tgrade\tnorm\torigin（由 0c-import 自證交所 ISIN 清單的上市、上櫃、興櫃股票簡稱產生；讀音以詞庫斷詞自動標注）")
}

// autoAnnotated 是讀音由 annotateNames 自動標注的來源。
var autoAnnotated = []string{"moi-places", "twse", "moe-schools", "nhi-hospitals", "moi-villages", "gov-agencies"}

// annotateNames 以目前的詞庫（拿掉只來自 origin 的詞）斷詞替名稱標音，詞庫已有的名稱略過，
// 寫出匯入暫存。專有名詞的破音字可能標錯，要靠差異報告審查。
func annotateNames(data, imp string, names map[string]bool, origin string, maxLen int, file, header string) string {
	d, errs, err := compile.Load(data)
	check(err)
	// 拿掉只來自自動標音名稱清單的詞（含這份清單自己）：每份清單只靠辭典來源與人工詞條標音，
	// 不受其他清單有沒有套用、套用先後影響，重跑匯入的結果才會穩定。
	d.Words = filterOrigins(d.Words, autoAnnotated)
	d.Reach = nil // 拿掉這些詞後，可達性回歸裡的名稱必然失敗；這份詞庫只用來標音
	o := compile.DefaultOptions()
	o.Unigram = "mcbopomofo"
	res := compile.Build(d, o)
	if len(errs)+len(res.Errors) > 0 {
		check(fmt.Errorf("標音用的詞庫編譯失敗：%v %v", errs, res.Errors))
	}
	rd := eval.NewReader(res.Lexicon, res.LM)
	inWords := importer.WordInternalReadings(d.Words, autoAnnotated...)
	var keys []string
	for n := range names {
		if n != "" && importer.IsHan(n) && len([]rune(n)) >= 2 && len([]rune(n)) <= maxLen {
			keys = append(keys, n)
		}
	}
	sort.Strings(keys)
	var out []fmt.Stringer
	known, added := 0, 0
	for _, n := range keys {
		if res.Lexicon.Has(n) {
			known++
			continue
		}
		r, segs, ok := rd.Segment(n)
		if !ok {
			continue
		}
		parts := make([]string, len(r))
		for i, s := range r {
			parts[i] = string(s)
		}
		var single []bool
		for _, w := range segs {
			k := len([]rune(w))
			for range k {
				single = append(single, k == 1)
			}
		}
		parts = inWords.Fix(n, parts, single)
		out = append(out, importer.WordLine{Word: n, Reading: strings.Join(parts, " "), Grade: "primary", Norm: "y", Origin: origin})
		added++
	}
	write(filepath.Join(imp, file), header, out)
	return fmt.Sprintf("%d 個，其中詞庫已有 %d、新增 %d", len(keys), known, added)
}

// filterOrigins 拿掉所有來源都在 origins 裡的詞。
func filterOrigins(ws []compile.WordReading, origins []string) []compile.WordReading {
	drop := map[string]bool{}
	for _, o := range origins {
		drop[o] = true
	}
	var out []compile.WordReading
	for _, w := range ws {
		for _, o := range w.Origins {
			if !drop[o] {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

// surfaces 回傳主詞庫與匯入暫存裡所有的詞面。
func surfaces(data, imp string) map[string]bool {
	out := map[string]bool{}
	files := []string{filepath.Join(data, "lexicon", "words.tsv"), filepath.Join(data, "lexicon", "chars.tsv")}
	more, _ := filepath.Glob(filepath.Join(imp, "*.tsv"))
	for _, p := range append(files, more...) {
		if base := filepath.Base(p); !strings.HasPrefix(base, "words") && !strings.HasPrefix(base, "chars") {
			continue
		}
		sc := bufio.NewScanner(open(p))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if t := sc.Text(); t != "" && !strings.HasPrefix(t, "#") {
				w, _, _ := strings.Cut(t, "\t")
				out[w] = true
			}
		}
	}
	return out
}

type line string

func (l line) String() string { return string(l) }

func open(p string) io.Reader {
	f, err := os.Open(p)
	check(err)
	return bufio.NewReader(f)
}

func write(p, header string, ls []fmt.Stringer) {
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, l := range ls {
		b.WriteString(l.String() + "\n")
	}
	check(os.WriteFile(p, []byte(b.String()), 0o644))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-import:", err)
		os.Exit(1)
	}
}
