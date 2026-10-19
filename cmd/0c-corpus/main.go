// 0c-corpus 由中文維基百科 dump 產生 n-gram 統計：抽取正文、以 OpenCC 轉台灣繁體、
// 以詞庫斷詞、計數。輸出 data/lm/unigram.tsv 的一欄 unigram 次數與 data/lm/bigram/ 的條件機率。
// -jsonl 改讀已是台灣繁體的 jsonl.gz 語料（每行一篇，取 title 與 text），不做 OpenCC 轉換。
//
// 評測句子中出現的片段會從語料剔除，避免評測污染（剔除數量會印出）。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/internal/compile"
	"git.biggo.com/0c/ime/internal/corpus"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/eval"
)

func main() {
	dump := flag.String("dump", "data/raw/zhwiki/zhwiki-latest-pages-articles.xml.bz2", "維基 dump（.xml.bz2）")
	jsonl := flag.String("jsonl", "", "改讀 jsonl.gz 語料（glob；每行一篇，取 title 與 text，不做 OpenCC 轉換）")
	occ := flag.String("opencc", "data/raw/opencc", "OpenCC 詞典目錄")
	pack := flag.String("vocab", "build/data", "提供詞表與斷詞用 unigram 的資料包")
	exclude := flag.String("exclude", "data/raw/cv/*.txt", "評測句子（glob）：語料中與其片段相同者剔除")
	origin := flag.String("origin", "zhwiki", "輸出的來源 ID")
	out := flag.String("data", "data", "輸出目錄")
	minCount := flag.Int("min-count", 2, "bigram 次數下限")
	splitSingle := flag.Bool("split-single", false, "bigram 分成兩份：<origin>-ns.tsv（至少一邊是多字詞）與 <origin>-ss.tsv（兩邊都是單字），讓編譯時分開給權重（docs/49 第 6 步）")
	maxPages := flag.Int("max-pages", 0, "只處理前 N 條條目（0 為全部）")
	workers := flag.Int("workers", runtime.NumCPU()-1, "平行處理的執行緒數")
	flag.Parse()

	var conv corpus.Converter
	if *jsonl == "" {
		var err error
		conv, err = corpus.S2TWP(*occ)
		check(err)
	}
	eng, err := datadir.Open(*pack)
	check(err)
	lex := eng.Model.Lex.(*lexicon.Compact)
	prob := map[string]float64{}
	lex.All(func(e lexicon.Entry) { prob[e.Surface] = eng.Model.LM.Uni(e.Surface) })
	vocab := corpus.NewVocab(prob)

	banned := map[string]bool{}
	files, _ := filepath.Glob(*exclude)
	for _, p := range files {
		f, err := os.Open(p)
		check(err)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// 只剔除 5 字以上的相同片段：「但是」「台灣」這類短片段在任何語料都會出現，不算污染；
			// 整句被原文收錄才是
			for _, c := range eval.Chunks(sc.Text(), 5) {
				banned[c] = true
			}
		}
		f.Close()
	}

	var cmd *exec.Cmd
	var stdout io.Reader
	if *jsonl == "" {
		cmd = exec.Command("bzip2", "-dc", *dump)
		pipe, err := cmd.StdoutPipe()
		check(err)
		check(cmd.Start())
		stdout = pipe
	}

	texts := make(chan string, 256)
	type batch [][]uint32
	batches := make(chan batch, 256)
	var pages, excluded, chunks atomic.Int64
	go func() {
		defer close(texts)
		n := 0
		emit := func(t string) {
			if *maxPages > 0 && n >= *maxPages {
				return
			}
			n++
			texts <- t
		}
		if *jsonl != "" {
			files, _ := filepath.Glob(*jsonl)
			sort.Strings(files)
			for _, p := range files {
				check(corpus.JSONLDocs(p, emit))
			}
			return
		}
		corpus.Pages(bufio.NewReaderSize(stdout, 1<<20), emit)
	}()
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range texts {
				var b batch
				if *jsonl == "" {
					t = conv.Convert(corpus.Clean(t))
				}
				corpus.HanChunks(t, 1, func(c string) {
					chunks.Add(1)
					if banned[c] {
						excluded.Add(1)
						return
					}
					vocab.Segment(c, func(ids []uint32) { b = append(b, append([]uint32(nil), ids...)) })
				})
				batches <- b
				pages.Add(1)
			}
		}()
	}
	go func() { wg.Wait(); close(batches) }()

	counts := corpus.NewCounts()
	start, last := time.Now(), time.Now()
	for b := range batches {
		for _, ids := range b {
			counts.Add(vocab, ids)
		}
		if time.Since(last) > 30*time.Second {
			last = time.Now()
			fmt.Fprintf(os.Stderr, "%v 條目 %d、片段 %d、bigram 種類 %d\n", time.Since(start).Round(time.Second), pages.Load(), counts.Chunk, len(counts.Bi))
		}
	}
	if cmd != nil {
		if *maxPages == 0 {
			check(cmd.Wait())
		} else {
			cmd.Process.Kill()
		}
	}

	// unigram 次數
	var ids []uint32
	var tokens uint64
	for id, n := range counts.Uni {
		ids = append(ids, id)
		tokens += n
	}
	sort.Slice(ids, func(a, b int) bool { return vocab.Name(ids[a]) < vocab.Name(ids[b]) })
	// 次數寫進 lm/unigram.tsv 的 <origin> 欄（整欄取代）
	up := filepath.Join(*out, "lm", "unigram.tsv")
	u, err := compile.ReadUnigram(up)
	check(err)
	col := make(map[string]string, len(ids))
	for _, id := range ids {
		col[vocab.Name(id)] = strconv.FormatUint(counts.Uni[id], 10)
	}
	u.SetColumn(*origin, col)
	check(u.Write(up))

	// bigram 條件機率 P(詞|前詞) = c(前詞,詞) / c(前詞 作為前詞)
	check(os.MkdirAll(filepath.Join(*out, "lm", "bigram"), 0o755))
	type sink struct {
		f *os.File
		w *bufio.Writer
	}
	open := func(name string) sink {
		f, err := os.Create(filepath.Join(*out, "lm", "bigram", name+".tsv"))
		check(err)
		w := bufio.NewWriter(f)
		fmt.Fprintf(w, "# origin: %s\n# 前詞\t詞\tP(詞|前詞)；次數 ≥ %d\n", name, *minCount)
		return sink{f, w}
	}
	var multi, single sink
	if *splitSingle {
		multi, single = open(*origin+"-ns"), open(*origin+"-ss")
	} else {
		multi = open(*origin)
		single = multi
	}
	kept := 0
	// 依（前詞, 詞）排序寫出，同一份語料與詞庫產生相同位元組
	keys := make([]uint64, 0, len(counts.Bi))
	for k, n := range counts.Bi {
		if int(n) >= *minCount {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(a, b int) bool {
		la, lb := vocab.Name(uint32(keys[a]>>32)), vocab.Name(uint32(keys[b]>>32))
		if la != lb {
			return la < lb
		}
		return vocab.Name(uint32(keys[a])) < vocab.Name(uint32(keys[b]))
	})
	for _, k := range keys {
		n := counts.Bi[k]
		l, w := uint32(k>>32), uint32(k)
		ln, wn := vocab.Name(l), vocab.Name(w)
		dst := multi
		if utf8.RuneCountInString(ln) == 1 && utf8.RuneCountInString(wn) == 1 {
			dst = single
		}
		fmt.Fprintf(dst.w, "%s\t%s\t%.6g\n", ln, wn, float64(n)/float64(counts.Left[l]))
		kept++
	}
	for _, s := range []sink{multi, single} {
		check(s.w.Flush())
	}
	check(multi.f.Close())
	if *splitSingle {
		check(single.f.Close())
	}
	fmt.Printf("條目 %d；片段 %d（與評測句子相同而剔除 %d）；詞 %d 種、%d 次；bigram %d 種，保留 %d（次數 ≥ %d）；耗時 %v\n",
		pages.Load(), chunks.Load(), excluded.Load(), len(counts.Uni), tokens, len(counts.Bi), kept, *minCount, time.Since(start).Round(time.Second))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-corpus:", err)
		os.Exit(1)
	}
}
