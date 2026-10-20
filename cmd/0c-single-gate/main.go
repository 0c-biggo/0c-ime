// 0c-single-gate 檢查整份字典的單字候選排序（#172）：非常用字集的字（只來自罕用來源，例如全字庫
// 獨有、不在 Big5 的日文新字體、簡化字、粵語用字）不得在任何常見前文下，排到同讀音、詞頻高 N 倍
// 以上的常用字前面。
//
// 前文取自資料包的 bigram：凡是「前詞→非常用字」有配對的前詞都打一次（另加句首），以 0c-server 同一套
// session 開單字候選，照使用者實際看到的順序比對。違規的字與已核可的名單（-approved）比較：多出新字
// 就失敗，名單要人工審過再更新（-write）。#171 的盤點（2,434 字、23,710 個前文）就是這個檢查。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/compile"
	"git.biggo.com/0c/ime/internal/datadir"
)

type scenario struct {
	left    string // 前詞；空字串為句首
	reading []zhuyin.Syllable
}

type violation struct {
	char, reading, common string
	left, typed           string // 前詞與實際組出的文字
	ratio                 float64
	at, commonAt          int
}

func main() {
	pack := flag.String("data", "build/data", "要檢查的資料包")
	source := flag.String("source", "data", "詞庫來源目錄（判斷哪些字只來自罕用來源）")
	rare := flag.String("rare-sources", "cns11643,mcbopomofo-rare", "只來自這些來源的字屬於非常用字集")
	factor := flag.Float64("factor", 10, "常用字的詞頻至少是非常用字的幾倍才算違規")
	approved := flag.String("approved", "", "已核可的違規字名單（字<TAB>讀音<TAB>備註）；有新字就失敗")
	write := flag.String("write", "", "把這次的違規字名單寫到檔案（人工核可後取代 -approved）")
	detail := flag.String("detail", "", "把每個違規情境寫成 TSV")
	workers := flag.Int("workers", runtime.NumCPU(), "平行開幾份引擎")
	limit := flag.Int("limit", 0, "只檢查前 N 個情境（0 為全部；除錯用）")
	sentences := flag.String("sentences", "", "改量句子評測集（glob）每個位置的單字候選名次，不做閘門檢查")
	reference := flag.String("reference-data", "", "-sentences 標音用的資料包（預設同 -data）")
	split := flag.String("split", "all", "-sentences 的子集：dev、test、all")
	flag.Parse()
	if *sentences != "" {
		runRank(*pack, *reference, *sentences, *split, *limit, *workers, *detail)
		return
	}

	d, problems, err := compile.Load(*source)
	check(err)
	if len(problems) > 0 {
		check(fmt.Errorf("%s 有 %d 個問題，例如 %v", *source, len(problems), problems[0]))
	}
	rareSrc := map[string]bool{}
	for _, x := range strings.Split(*rare, ",") {
		if x = strings.TrimSpace(x); x != "" {
			rareSrc[x] = true
		}
	}
	// 非常用字：每個讀音的來源都只在罕用來源裡（規則推得的讀音不算來源）。
	commonChar := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range d.Chars {
		seen[c.Char] = true
		for _, x := range c.Origins {
			if !rareSrc[x] && !strings.HasPrefix(x, "rule:") {
				commonChar[c.Char] = true
			}
		}
	}
	for _, w := range d.Words {
		if utf8.RuneCountInString(w.Word) == 1 {
			commonChar[w.Word] = true
		}
	}
	isRare := func(ch string) bool { return seen[ch] && !commonChar[ch] }

	eng, err := datadir.Open(*pack)
	check(err)
	lex := eng.Model.Lex.(*lexicon.Compact)
	sys, ok := eng.Model.LM.(*lm.Compact)
	if !ok {
		check(fmt.Errorf("%s 的語言模型不是資料包格式", *pack))
	}
	// 每個讀音的單字候選中，有沒有非常用字、又有詞頻高 N 倍的常用字；沒有的讀音不必打。
	type syl struct {
		rare   []string
		common []string
	}
	bySyl := map[zhuyin.Syllable]*syl{}
	lex.All(func(e lexicon.Entry) {
		if len(e.Reading) != 1 || e.Correction || utf8.RuneCountInString(e.Surface) != 1 {
			return
		}
		s := bySyl[e.Reading[0]]
		if s == nil {
			s = &syl{}
			bySyl[e.Reading[0]] = s
		}
		if isRare(e.Surface) {
			s.rare = append(s.rare, e.Surface)
		} else {
			s.common = append(s.common, e.Surface)
		}
	})
	risky := map[string][]zhuyin.Syllable{} // 非常用字 → 需要檢查的讀音
	for r, s := range bySyl {
		top := 0.0
		for _, w := range s.common {
			top = max(top, sys.Uni(w))
		}
		for _, w := range s.rare {
			if top >= *factor*sys.Uni(w) {
				risky[w] = append(risky[w], r)
			}
		}
	}
	// 前文：句首，以及每個與非常用字有 bigram 的前詞（取詞庫裡讀音權重最高的打法）。
	readingOf := func(w string) []zhuyin.Syllable {
		var best lexicon.Entry
		for _, e := range lex.Lookup(w) {
			if !e.Correction && e.PRW > best.PRW {
				best = e
			}
		}
		return best.Reading
	}
	set := map[string]scenario{}
	add := func(left string, r zhuyin.Syllable) {
		var reading []zhuyin.Syllable
		if left != "" {
			if reading = readingOf(left); reading == nil {
				return
			}
		}
		reading = append(append([]zhuyin.Syllable(nil), reading...), r)
		set[zhuyin.Key(reading)] = scenario{left, reading}
	}
	for _, rs := range risky {
		for _, r := range rs {
			add("", r)
		}
	}
	sys.EachBigram(func(left, w string, p float64) {
		if left == lm.BOS {
			return
		}
		for _, r := range risky[w] {
			add(left, r)
		}
	})
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if *limit > 0 && len(keys) > *limit {
		keys = keys[:*limit]
	}
	fmt.Fprintf(os.Stderr, "非常用字 %d 個需要檢查；情境 %d 個\n", len(risky), len(keys))

	jobs := make(chan scenario)
	var mu sync.Mutex
	var found []violation
	var wg sync.WaitGroup
	for w := 0; w < max(1, *workers); w++ {
		e := eng
		if w > 0 {
			e, err = datadir.Open(*pack)
			check(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := e.OpenSession()
			s := e.Session(id)
			for sc := range jobs {
				s.Handle(proto.Request{Type: proto.Cancel})
				s.Handle(proto.Request{Type: proto.Pause, On: true})
				var snap proto.Snapshot
				for _, y := range sc.reading {
					for _, k := range layout.Standard.Keys(y) {
						snap = s.Handle(proto.Request{Type: proto.Key, Key: string(k)})
					}
				}
				typed := snap.Text()
				snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
				if snap.Candidates == nil {
					continue
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
				r := sc.reading[len(sc.reading)-1]
				var local []violation
				for i, x := range items {
					if !isRare(x) {
						continue
					}
					ux := sys.Uni(x)
					for j := i + 1; j < len(items); j++ {
						y := items[j]
						if isRare(y) || utf8.RuneCountInString(y) != 1 {
							continue
						}
						if uy := sys.Uni(y); uy >= *factor*ux {
							local = append(local, violation{x, string(r), y, sc.left, typed, uy / ux, i, j})
							break // 每個字在每個情境記第一個被超前的常用字
						}
					}
				}
				if len(local) > 0 {
					mu.Lock()
					found = append(found, local...)
					mu.Unlock()
				}
			}
		}()
	}
	for _, k := range keys {
		jobs <- set[k]
	}
	close(jobs)
	wg.Wait()

	type charKey struct{ char, reading string }
	byChar := map[charKey][]violation{}
	contexts := map[string]bool{}
	for _, v := range found {
		k := charKey{v.char, v.reading}
		byChar[k] = append(byChar[k], v)
		contexts[v.left+"\t"+v.reading] = true
	}
	list := make([]charKey, 0, len(byChar))
	for k := range byChar {
		list = append(list, k)
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].reading != list[b].reading {
			return list[a].reading < list[b].reading
		}
		return list[a].char < list[b].char
	})
	noLeft := 0
	for _, v := range found {
		if v.left == "" {
			noLeft++
		}
	}
	fmt.Printf("違規：%d 個字（字×讀音），%d 個前文情境，其中 %d 個不需要前文（句首）\n", len(list), len(contexts), noLeft)

	if *detail != "" {
		f, err := os.Create(*detail)
		check(err)
		w := bufio.NewWriter(f)
		fmt.Fprintln(w, "# 字\t讀音\t前詞\t組字\t排在它後面的常用字\t詞頻倍數\t名次\t常用字名次")
		sort.Slice(found, func(a, b int) bool {
			if found[a].reading != found[b].reading {
				return found[a].reading < found[b].reading
			}
			if found[a].char != found[b].char {
				return found[a].char < found[b].char
			}
			return found[a].left < found[b].left
		})
		for _, v := range found {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.0f\t%d\t%d\n", v.char, v.reading, v.left, v.typed, v.common, v.ratio, v.at+1, v.commonAt+1)
		}
		check(w.Flush())
		check(f.Close())
	}
	if *write != "" {
		f, err := os.Create(*write)
		check(err)
		w := bufio.NewWriter(f)
		fmt.Fprintln(w, "# 單字排序閘門（0c-single-gate）已核可的違規字：非常用字集的字在某些前文下排在詞頻高很多的同音常用字前面。")
		fmt.Fprintln(w, "# 每次重建字典都檢查；出現不在這份名單的字就失敗，人工確認後以 -write 重寫。")
		fmt.Fprintln(w, "# 字\t讀音\t情境數與例子（不比對）")
		for _, k := range list {
			v := byChar[k][0]
			fmt.Fprintf(w, "%s\t%s\t%d 個情境，例如 %s→%s 排在 %s 前面\n", k.char, k.reading, len(byChar[k]), v.left, v.char, v.common)
		}
		check(w.Flush())
		check(f.Close())
	}
	if *approved == "" {
		return
	}
	allowed := map[charKey]bool{}
	f, err := os.Open(*approved)
	check(err)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			check(fmt.Errorf("%s：格式應為 字<TAB>讀音：%q", *approved, line))
		}
		allowed[charKey{parts[0], parts[1]}] = true
	}
	check(sc.Err())
	f.Close()
	var added, gone []string
	for _, k := range list {
		if !allowed[k] {
			v := byChar[k][0]
			added = append(added, fmt.Sprintf("%s %s（%d 個情境，例如 %s→%s 排在 %s 前面，詞頻差 %.0f 倍）", k.char, k.reading, len(byChar[k]), v.left, v.char, v.common, v.ratio))
		}
	}
	for k := range allowed {
		if len(byChar[k]) == 0 {
			gone = append(gone, k.char+" "+k.reading)
		}
	}
	sort.Strings(gone)
	if len(gone) > 0 {
		fmt.Printf("名單上 %d 個字已不再違規（可以從名單拿掉）：%s\n", len(gone), strings.Join(gone, "、"))
	}
	if len(added) > 0 {
		fmt.Printf("新的違規字 %d 個（不在 %s）：\n", len(added), *approved)
		for _, a := range added {
			fmt.Println("  " + a)
		}
		os.Exit(1)
	}
	fmt.Println("單字排序閘門通過")
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-single-gate:", err)
		os.Exit(1)
	}
}
