package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/eval"
)

// rankStats 是一組位置上參考字在單字候選的名次分布。
type rankStats struct {
	n, first, page, missing int
	sum                     int // 名次總和（找不到算 51）
}

func (r *rankStats) add(rank int) {
	r.n++
	switch {
	case rank <= 0:
		r.missing++
		r.sum += 51
		return
	case rank == 1:
		r.first++
	}
	if rank <= 10 {
		r.page++
	}
	r.sum += min(rank, 51)
}

func (r rankStats) String() string {
	if r.n == 0 {
		return "0 個位置"
	}
	return fmt.Sprintf("%d 個位置：第一名 %.1f%%、第一頁 %.1f%%、平均名次 %.2f、找不到 %d",
		r.n, 100*float64(r.first)/float64(r.n), 100*float64(r.page)/float64(r.n), float64(r.sum)/float64(r.n), r.missing)
}

// runRank 量句子評測集裡每個位置的單字候選：組字錯的位置，參考字排第幾（要改字時要翻多遠）；
// 組字對的位置，目前的字排第幾。另外列出錯字位置上「扣掉目前組字的字」的名次。
func runRank(pack, reference, glob, split string, limit, workers int, out string) {
	eng, err := datadir.Open(pack)
	check(err)
	ref := eng
	if reference != "" {
		ref, err = datadir.Open(reference)
		check(err)
	}
	rd := eval.NewReader(ref.Model.Lex.(*lexicon.Compact), ref.Model.LM)
	var chunks []string
	files, _ := filepath.Glob(glob)
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
	chunks = slices.Compact(chunks)
	chunks = eval.Split(chunks, split)
	rand.New(rand.NewSource(1)).Shuffle(len(chunks), func(i, j int) { chunks[i], chunks[j] = chunks[j], chunks[i] })
	if limit > 0 && len(chunks) > limit {
		chunks = chunks[:limit]
	}
	type job struct {
		text    string
		reading []zhuyin.Syllable
	}
	type result struct {
		text, typed string
		pos         int
		want, got   string
		rank, other int
	}
	jobs := make(chan job)
	var mu sync.Mutex
	var results []result
	var wg sync.WaitGroup
	for w := 0; w < max(1, workers); w++ {
		e := eng
		if w > 0 {
			e, err = datadir.Open(pack)
			check(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := e.Session(e.OpenSession())
			for j := range jobs {
				s.Handle(proto.Request{Type: proto.Cancel})
				s.Handle(proto.Request{Type: proto.Pause, On: true})
				var snap proto.Snapshot
				for _, y := range j.reading {
					for _, r := range string(y) {
						snap = s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(r)})
					}
					if y.ToneOf() == 1 {
						snap = s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(zhuyin.Tone1)})
					}
				}
				typed := []rune(snap.Text())
				want := []rune(j.text)
				if len(typed) != len(want) {
					continue
				}
				var local []result
				for i := range want {
					s.Handle(proto.Request{Type: proto.Cursor, To: i})
					snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
					if snap.Candidates == nil || snap.Candidates.Start != i { // 前方模式：游標在字的後面
						if snap.Candidates != nil {
							s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEscape})
						}
						s.Handle(proto.Request{Type: proto.Cursor, To: i + 1})
						snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
						if snap.Candidates == nil || snap.Candidates.Start != i {
							if snap.Candidates != nil {
								s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEscape})
							}
							continue
						}
					}
					var items []string
					for p := 0; p < snap.Candidates.Pages; p++ {
						page := s.Handle(proto.Request{Type: proto.Page, Index: p})
						if page.Candidates == nil {
							break
						}
						for _, it := range page.Candidates.Items {
							items = append(items, it.Text)
						}
					}
					s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEscape})
					rank := slices.Index(items, string(want[i])) + 1
					other := 0 // 扣掉目前組字的字之後的名次
					if rank > 0 {
						other = rank
						if k := slices.Index(items, string(typed[i])); k >= 0 && k+1 < rank {
							other--
						}
					}
					local = append(local, result{j.text, string(typed), i, string(want[i]), string(typed[i]), rank, other})
				}
				mu.Lock()
				results = append(results, local...)
				mu.Unlock()
			}
		}()
	}
	skipped := 0
	for _, c := range chunks {
		reading, ok := rd.Annotate(c)
		if !ok {
			skipped++
			continue
		}
		jobs <- job{c, reading}
	}
	close(jobs)
	wg.Wait()
	var wrong, wrongOther, right rankStats
	for _, r := range results {
		if r.want != r.got {
			wrong.add(r.rank)
			wrongOther.add(r.other)
		} else {
			right.add(r.rank)
		}
	}
	fmt.Printf("句子片段 %d（無法標音略過 %d）\n", len(chunks)-skipped, skipped)
	fmt.Printf("組字錯的位置，參考字的名次：%v\n", wrong)
	fmt.Printf("　同上，扣掉目前組字的字：%v\n", wrongOther)
	fmt.Printf("組字對的位置，目前的字的名次：%v\n", right)
	if out == "" {
		return
	}
	f, err := os.Create(out)
	check(err)
	w := bufio.NewWriter(f)
	sort.Slice(results, func(a, b int) bool {
		if results[a].text != results[b].text {
			return results[a].text < results[b].text
		}
		return results[a].pos < results[b].pos
	})
	fmt.Fprintln(w, "# 參考\t組字\t位置\t參考字\t組字的字\t名次\t扣掉組字的名次")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%d\t%d\n", r.text, r.typed, r.pos, r.want, r.got, r.rank, r.other)
	}
	check(w.Flush())
	check(f.Close())
}
