// 0c-eval 量測資料包的選詞品質、逐鍵行為、延遲與記憶體。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"git.biggo.com/0c/ime/core/decode"
	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/vocab"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/eval"
)

func main() {
	data := flag.String("data", "build/data", "資料包目錄")
	referenceData := flag.String("reference-data", "", "固定參考標音的資料包；比較詞庫／詞頻版本時使用同一份（預設同 -data）")
	sentences := flag.String("sentences", "data/raw/cv/*.txt", "評測句子（glob）")
	limit := flag.Int("limit", 3000, "評測片段數上限（隨機抽樣，seed 固定）")
	cases := flag.String("cases", "testdata/cases", "田野案例目錄")
	quiet := flag.Bool("summary", false, "只印一行摘要")
	cpuprof := flag.String("cpuprofile", "", "把 CPU 剖析寫到檔案")
	split := flag.String("split", "test", "句子片段的子集：dev（調參用，約 1/5）、test（報告用）、all")
	mu := flag.Float64("mu", -1, "系統 bigram 與 unigram 的內插權重（預設用 personal.DefaultParams）")
	minConf := flag.Float64("minconf", -1, "Tab 補全的信心門檻（預設用 suggest.DefaultOptions）")
	attentionCost := flag.Float64("attention-cost", -1, "檢視補全的等效按鍵成本（0 停用效益門檻）")
	rewritePrefix := flag.Bool("rewrite-prefix", false, "實驗：主動顯示同音替換預覽（預設只由 Tab 要求）")
	rewriteConf := flag.Float64("rewrite-minconf", -1, "同音替換的條件信心門檻（預設用 suggest.DefaultOptions）")
	beam := flag.Int("beam", -1, "每個位置展開的有上下文狀態上限（預設用 session.DefaultOptions）")
	errFile := flag.String("errors", "", "把所有錯誤片段（參考、輸出、錯誤類型）寫到檔案")
	cont := flag.Float64("continue", -1, "尾端「還會繼續打」的權重 γ（預設用 session.DefaultOptions）")
	reportFile := flag.String("report", "", "輸出含版本、資料雜湊與逐鍵回改分類的 JSON 報告")
	separate := flag.Bool("separate-continuation", true, "完整詞與未完成長詞分開競爭（false 可重跑舊前綴公式）")
	vocabFile := flag.String("vocab", "", "載入自訂詞彙檔（vocab.json），量自訂詞對一般句子的影響（#122）")
	vocabAdd := flag.Bool("vocab-add", false, "以 -vocab 的詞逐批「加入」（同使用者親手加詞，會學習），而不是直接載入檔案")
	flag.Parse()
	if *cpuprof != "" {
		f, err := os.Create(*cpuprof)
		check(err)
		check(pprof.StartCPUProfile(f))
		defer pprof.StopCPUProfile()
	}

	runtime.GC()
	start := time.Now()
	eng, err := datadir.Open(*data)
	if err == nil && *vocabFile != "" {
		if _, statErr := os.Stat(*vocabFile); statErr != nil {
			err = statErr
		} else {
			err = datadir.LoadVocab(eng, *vocabFile)
		}
		if err == nil && *vocabAdd {
			// 取出詞後清空，再以 VocabAdd 分批加入，走跟使用者加詞相同的學習路徑。
			words := eng.VocabSnapshot().Words()
			eng.SetVocab(nil)
			for i := 0; i < len(words) && err == nil; i += vocab.MaxBatchItems {
				var entries []vocab.Entry
				for _, w := range words[i:min(len(words), i+vocab.MaxBatchItems)] {
					entries = append(entries, vocab.Entry{Text: w.Text, Readings: w.Readings})
				}
				_, err = eng.Model.VocabAdd(entries, vocab.SourceManual)
			}
		}
	}
	check(err)
	loadTime := time.Since(start)
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	lex, ok := eng.Model.Lex.(*lexicon.Compact)
	if !ok {
		check(fmt.Errorf("評測需要編譯後的資料包"))
	}
	rd := eval.NewReader(lex, eng.Model.LM)
	if *referenceData != "" {
		reference, err := datadir.Open(*referenceData)
		check(err)
		referenceLex, ok := reference.Model.Lex.(*lexicon.Compact)
		if !ok {
			check(fmt.Errorf("參考標音需要編譯後的資料包"))
		}
		rd = eval.NewReader(referenceLex, reference.Model.LM)
	}
	if *mu >= 0 {
		eng.Model.P.Mu = *mu
	}

	// ---------------------------------------------------------------- 句子
	var chunks []string
	files, _ := filepath.Glob(*sentences)
	for _, p := range files {
		f, err := os.Open(p)
		check(err)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			chunks = append(chunks, eval.Chunks(sc.Text(), 2)...)
		}
		f.Close()
	}
	sort.Strings(chunks)
	chunks = dedupe(chunks)
	chunks = eval.Split(chunks, *split)
	rand.New(rand.NewSource(1)).Shuffle(len(chunks), func(i, j int) { chunks[i], chunks[j] = chunks[j], chunks[i] })
	ty := eval.NewTypist(eng)
	eng.Session(ty.ID).Opt.SeparateContinuation = *separate
	if *minConf >= 0 {
		eng.Session(ty.ID).Opt.Suggest.MinConf = *minConf
	}
	if *attentionCost >= 0 {
		eng.Session(ty.ID).Opt.Suggest.AttentionCost = *attentionCost
	}
	eng.Session(ty.ID).Opt.Suggest.RewritePrefix = *rewritePrefix
	if *rewriteConf >= 0 {
		eng.Session(ty.ID).Opt.Suggest.MinRewriteConf = *rewriteConf
	}
	if *beam >= 0 {
		eng.Session(ty.ID).Opt.Beam = *beam
	}
	if *cont >= 0 {
		eng.Session(ty.ID).Opt.Continue = *cont
	}
	var n, right, rightForm, chars, edits, uncovered, stableSum, rewrites int
	var stableRatio float64
	var wrong []string
	var searchErr, modelErr int
	var errOut *bufio.Writer
	if *errFile != "" {
		f, err := os.Create(*errFile)
		check(err)
		defer f.Close()
		errOut = bufio.NewWriter(f)
		defer errOut.Flush()
	}
	dec := &decode.Decoder{Lex: eng.Model.Source(), Score: eng.Model, Beam: eng.Session(ty.ID).Opt.Beam, Continue: eng.Session(ty.ID).Opt.Continue}
	dec.SeparateContinuation = *separate
	for _, c := range chunks {
		if n >= *limit {
			break
		}
		reading, ok := rd.Annotate(c)
		if !ok {
			uncovered++
			continue
		}
		final, stable, rw := ty.Replay(c, reading)
		n++
		chars += len([]rune(c))
		e := eval.EditDistance(c, final)
		edits += e
		if e == 0 {
			right++
		}
		if formFold(c) == formFold(final) {
			rightForm++
		} else {
			// 以參考斷詞鎖住每個詞，求正確答案在模型下的分數；比輸出高就是搜尋錯誤
			lat := input.Exact{}.Expand(reading)
			_, outScore := dec.Best(lat, nil, true)
			_, words, _ := rd.Segment(c)
			locks := map[int]decode.Lock{}
			pos := 0
			for _, w := range words {
				n := len([]rune(w))
				locks[pos] = decode.Lock{Word: w, Len: n}
				pos += n
			}
			_, refScore := dec.Best(lat, locks, false)
			kind := "模型"
			if refScore > outScore+1e-9 {
				kind = "搜尋"
				searchErr++
			} else {
				modelErr++
			}
			if len(wrong) < 25 {
				wrong = append(wrong, fmt.Sprintf("%s → %s（%s）", c, final, kind))
			}
			if errOut != nil {
				fmt.Fprintf(errOut, "%s\t%s\t%s\t%s\t%.2f\n", c, final, kind, strings.Join(words, " "), refScore-outScore)
			}
		}
		stableSum += stable
		stableRatio += float64(stable) / float64(len(reading))
		rewrites += rw
	}

	// ---------------------------------------------------------------- 田野案例
	type fc struct{ id, typed, alt, want, layer string }
	var fcs []fc
	readTSV(filepath.Join(*cases, "field_cases.tsv"), func(f []string) {
		fcs = append(fcs, fc{f[0], f[1], f[2], f[3], f[5]})
	})
	var fieldLines []string
	fieldPass := 0
	for _, c := range fcs {
		got := typeAll(ty, c.typed)
		res := "✓"
		if got != c.want {
			res = "✗"
		} else {
			fieldPass++
		}
		line := fmt.Sprintf("  %s %-4s %-24s → %-8s 應為 %-8s [%s]", res, c.id, c.typed, got, c.want, c.layer)
		if c.alt != "" {
			if alt := typeAll(ty, c.alt); alt != c.want {
				line += fmt.Sprintf("（另一打法 %s → %s）", c.alt, alt)
			}
		}
		fieldLines = append(fieldLines, line)
	}

	// ---------------------------------------------------------------- 易混字
	type cp struct{ id, set, a, ansA, b, ansB string }
	var cps []cp
	readTSV(filepath.Join(*cases, "confusion_pairs.tsv"), func(f []string) {
		cps = append(cps, cp{f[0], f[1], f[3], f[4], f[5], f[6]})
	})
	var confTotal, confFull, confNow int
	var confLines []string
	for _, c := range cps {
		for _, side := range [][2]string{{c.a, c.ansA}, {c.b, c.ansB}} {
			text := strings.Replace(side[0], "＿", side[1], 1)
			at := len([]rune(side[0][:strings.Index(side[0], "＿")]))
			var hanOnly []rune
			pos := -1
			for i, r := range []rune(text) {
				if i == at {
					pos = len(hanOnly)
				}
				if eval.IsHan(r) {
					hanOnly = append(hanOnly, r)
				}
			}
			reading, ok := rd.Annotate(string(hanOnly))
			if !ok || pos < 0 {
				confLines = append(confLines, fmt.Sprintf("  ? %s %s：無法標音", c.id, text))
				continue
			}
			confTotal++
			n := len([]rune(side[1]))
			full := typeReading(ty, reading)
			now := typeShown(ty, reading[:pos+n])
			okFull := sub(full, pos, n) == side[1]
			okNow := sub(now, pos, n) == side[1]
			if okFull {
				confFull++
			}
			if okNow {
				confNow++
			}
			if !okFull || !okNow {
				confLines = append(confLines, fmt.Sprintf("  %s %s：整句 %s／打到當下 %s", c.id, text, mark(okFull, full), mark(okNow, now)))
			}
		}
	}

	// ---------------------------------------------------------------- 報告
	if *referenceData != "" {
		fmt.Printf("參考標音資料包：%s\n", *referenceData)
	}
	rss := vmRSS()
	pct := func(p float64) string { return eval.Percentile(ty.Latency, p).Round(time.Microsecond).String() }
	if *reportFile != "" {
		check(eval.WriteReport(*reportFile, map[string]any{
			"runtime": eval.Provenance(*data), "reference": eval.ProvenanceData(*referenceData, *data),
			"split": *split, "segments": n, "characters": chars, "correct": right, "edits": edits,
			"rewrites": ty.Rewrites, "options": eng.Session(ty.ID).Opt, "personal_loaded": false,
			"tab": ty.Tab, "latency": map[string]string{"p50": pct(50), "p95": pct(95), "p99": pct(99), "max": pct(100)},
			"field_pass": fieldPass, "field_total": len(fcs), "uncovered": uncovered,
		}))
	}
	if *quiet {
		fmt.Printf("%s[%s]\t片段 %d\t整段正確 %.1f%%（詞形同 %.1f%%）\t錯誤 模型 %d 搜尋 %d\tCER %.2f%%\t穩定 %.2f\t回改/百字 %.1f\t田野 %d/%d\t易混 整句 %d/%d 當下 %d/%d\tp50 %s p99 %s\n",
			*data, *split, n, 100*float64(right)/float64(n), 100*float64(rightForm)/float64(n), modelErr, searchErr, 100*float64(edits)/float64(chars), stableRatio/float64(n),
			100*float64(rewrites)/float64(chars), fieldPass, len(fcs), confFull, confTotal, confNow, confTotal, pct(50), pct(99))
		return
	}
	fmt.Printf("== 資料包 %s\n詞條 %d；載入 %v；Go 堆積 %.1f MiB；RSS %s\n\n", *data, lex.Len(), loadTime.Round(time.Millisecond), float64(ms.HeapAlloc)/(1<<20), rss)
	fmt.Printf("== 句子片段（Common Voice zh-TW，%s 子集，%d 段，%d 字；無法以詞庫標音而略過 %d 段）\n", *split, n, chars, uncovered)
	fmt.Printf("整段首選正確：%.1f%%（%d/%d）；視詞形變體（台／臺等）為相同：%.1f%%\nCER：%.2f%%\n",
		100*float64(right)/float64(n), right, n, 100*float64(rightForm)/float64(n), 100*float64(edits)/float64(chars))
	fmt.Printf("首次穩定正確位置：平均在第 %.1f 個音節、占全長 %.2f（越小越早正確）\n每百字回改：%.1f 字\n", float64(stableSum)/float64(n), stableRatio/float64(n), 100*float64(rewrites)/float64(chars))
	tb := ty.Tab
	fmt.Printf("逐音節回改分類：比較 %d 個既有位置；正確→錯誤 %d、錯誤→正確 %d、錯誤→其他錯誤 %d\n", ty.Rewrites.Compared, ty.Rewrites.CorrectToWrong, ty.Rewrites.WrongToCorrect, ty.Rewrites.WrongToWrong)
	fmt.Printf("Tab 補全：每百音節出現 %.1f 次建議，其中正確 %.0f%%；接受正確建議可省 %.1f%% 的按鍵（%.1f%% 的音節）\n",
		100*float64(tb.Shown)/float64(max(1, tb.Syllables)), 100*float64(tb.Correct)/float64(max(1, tb.Shown)),
		100*float64(tb.SavedKeys)/float64(max(1, tb.Keys)), 100*float64(tb.SavedSyllables)/float64(max(1, tb.Syllables)))
	fmt.Printf("錯誤類型：模型錯誤 %d（模型認為輸出比正確答案好）、搜尋錯誤 %d（正確答案分數較高，但搜尋漏掉了）\n", modelErr, searchErr)
	fmt.Println("錯誤例（參考 → 輸出；不含只差詞形變體者）：")
	for _, w := range wrong {
		fmt.Println("  " + w)
	}
	fmt.Printf("\n== 延遲（%d 個請求，每個注音符號一個）\np50 %s  p95 %s  p99 %s  最大 %s\n", len(ty.Latency), pct(50), pct(95), pct(99), pct(100))
	fmt.Printf("\n== 田野案例：%d/%d 首選正確（未學習的全新使用者）\n", fieldPass, len(fcs))
	for _, l := range fieldLines {
		fmt.Println(l)
	}
	fmt.Printf("\n== 易混字（%d 句）：整句打完 %d/%d，打到該字當下 %d/%d\n", confTotal, confFull, confTotal, confNow, confTotal)
	for _, l := range confLines {
		fmt.Println(l)
	}
}

// formFold 把常見的詞形變體折成同一字，供「只差詞形」的判斷（docs/13 詞形變體）。
// 清單刻意很短：只收台灣兩種寫法都通行、輸入法選哪個都不算錯的字。
var formFold = strings.NewReplacer("臺", "台", "裏", "裡", "峯", "峰", "綫", "線").Replace

func sub(s string, pos, n int) string {
	r := []rune(s)
	if pos+n > len(r) {
		return ""
	}
	return string(r[pos : pos+n])
}

func mark(ok bool, s string) string {
	if ok {
		return "✓"
	}
	return "✗" + s
}

// typeReading 打完並提交，回傳提交的文字（使用者打完就是要這個結果）。
func typeReading(ty *eval.Typist, r []zhuyin.Syllable) string {
	for _, s := range r {
		ty.Type(s)
	}
	return ty.Commit()
}

// typeShown 打完但不提交，回傳組字區顯示的文字（打到當下看到的樣子）。
func typeShown(ty *eval.Typist, r []zhuyin.Syllable) string {
	var out string
	for _, s := range r {
		out = ty.Type(s).Text()
	}
	ty.Cancel()
	return out
}

func typeAll(ty *eval.Typist, reading string) string {
	return typeReading(ty, zhuyin.ParseReading(reading))
}

func readTSV(p string, fn func([]string)) {
	f, err := os.Open(p)
	check(err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	for first := true; sc.Scan(); first = false {
		if first || strings.HasPrefix(sc.Text(), "#") || sc.Text() == "" {
			continue
		}
		fn(strings.Split(sc.Text(), "\t"))
	}
}

func dedupe(xs []string) []string {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func vmRSS() string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "（無法取得）"
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return "?"
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-eval:", err)
		os.Exit(1)
	}
}
