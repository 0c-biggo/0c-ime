// 0c-personal-sim 模擬一位使用者的打字歷史，再量學到的個人資料對之後文字與一般文字的影響（#218）。
//
// 歷史文字逐句以注音打完；與參考答案不同時，模擬使用者把游標移到第一個打錯的詞、開候選、選對的詞，
// 直到整句正確（或改不動）才提交，學習照常進行。-miss 是「沒注意到錯字、直接送出」的句子比例，
// 用來模擬沒改就送出的錯字被學進去（#218 的第三點）。每 -per-day 句把時鐘往後推一天，衰減照常。
//
// 打完歷史後，以學到的個人資料（暫停學習）逐句重打 -cases 的文字，依類別統計整句正確率；
// -save-user 另存學到的個人資料（含使用者文字，只留本機）。同一份輸入、同一個 -seed 結果可重現。
//
// 只用統計選字：神經選字由宿主提供，不在這裡。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

type kase struct{ Cat, Text string }

func readCases(path string) ([]kase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []kase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		cat, text, ok := strings.Cut(line, "\t")
		if !ok {
			cat, text = "history", line
		}
		if text != "" {
			out = append(out, kase{cat, text})
		}
	}
	return out, sc.Err()
}

// user 是在一個會學習的 session 上打字、改錯的模擬使用者。
type user struct {
	e      *engine.Engine
	id     uint64
	reader eval.Reader
	// 統計
	sentences, typed, clean, picks, unfixed, missed int
}

func (u *user) do(r proto.Request) proto.Snapshot {
	s, err := u.e.Do(u.id, r)
	if err != nil {
		panic(err)
	}
	return s
}

func (u *user) typeSyllable(y zhuyin.Syllable) proto.Snapshot {
	var snap proto.Snapshot
	for _, r := range string(y) {
		snap = u.do(proto.Request{Type: proto.Symbol, Symbol: string(r)})
	}
	if y.ToneOf() == 1 {
		snap = u.do(proto.Request{Type: proto.Symbol, Symbol: string(zhuyin.Tone1)})
	}
	return snap
}

// pick 在游標 start 開長度 n 的候選，找到 word 就選它。
func (u *user) pick(start, n int, word string) bool {
	u.do(proto.Request{Type: proto.Cursor, To: start})
	snap := u.do(proto.Request{Type: proto.Candidates, Length: n})
	c := snap.Candidates
	if c == nil || c.Start != start || c.Length != n {
		u.do(proto.Request{Type: proto.Close})
		return false
	}
	for page := 0; page < c.Pages; page++ {
		if page > 0 {
			snap = u.do(proto.Request{Type: proto.Page, Index: page})
			c = snap.Candidates
			if c == nil {
				return false
			}
		}
		for _, it := range c.Items {
			if it.Text == word {
				u.do(proto.Request{Type: proto.Select, Index: it.ID})
				return true
			}
		}
	}
	u.do(proto.Request{Type: proto.Close})
	return false
}

// write 打一句、依需要改錯後提交。miss 為真時看到錯字也直接送出。
func (u *user) write(text string, miss bool) {
	syls, words, ok := u.reader.Segment(text)
	if !ok {
		return
	}
	u.sentences++
	var snap proto.Snapshot
	for _, y := range syls {
		snap = u.typeSyllable(y)
	}
	u.typed += len(syls)
	want := []rune(text)
	if snap.Text() == text {
		u.clean++
	} else if miss {
		u.missed++
	} else {
		for round := 0; round < 2*len(words) && snap.Text() != text; round++ {
			got := []rune(snap.Text())
			fixed := false
			for pos, k := 0, 0; k < len(words); k++ {
				n := len([]rune(words[k]))
				if len(got) < pos+n || string(got[pos:pos+n]) != words[k] {
					fixed = u.pick(pos, n, words[k])
					if !fixed && n > 1 { // 整詞找不到：逐字改
						for i := pos; i < pos+n; i++ {
							if i >= len(got) || got[i] != want[i] {
								fixed = u.pick(i, 1, string(want[i])) || fixed
							}
						}
					}
					if fixed {
						u.picks++
					}
					break
				}
				pos += n
			}
			snap = u.do(proto.Request{Type: proto.Cursor, To: len(syls)})
			if !fixed {
				break
			}
		}
		if snap.Text() != text {
			u.unfixed++
		}
	}
	u.do(proto.Request{Type: proto.Commit})
}

type result struct {
	Cat  string `json:"cat"`
	Want string `json:"want"`
	Got  string `json:"got"`
}

func main() {
	data := flag.String("data", "build/data", "資料包目錄")
	start := flag.String("user", "", "起始個人資料（唯讀）；空白為空白個人資料")
	history := flag.String("history", "", "歷史文字：每行一句，或「類別<TAB>文字」")
	casesFile := flag.String("cases", "", "評測案例 TSV：類別<TAB>文字")
	miss := flag.Float64("miss", 0.1, "看到錯字也直接送出的句子比例")
	seed := flag.Int64("seed", 1, "亂數種子")
	perDay := flag.Int("per-day", 200, "每打幾句把時鐘往後推一天")
	saveUser := flag.String("save-user", "", "另存學到的個人資料（不覆蓋既有檔案）")
	details := flag.String("details", "", "每個案例的結果 JSONL（不覆蓋既有檔案）")
	workers := flag.Int("workers", 8, "評測的平行引擎數")
	habitLift := flag.Float64("habit-lift", -1, "讀音習慣的分數上限；負值用預設，0 停用（#218）")
	flag.Parse()
	check := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	e, err := datadir.Open(*data)
	check(err)
	if *start != "" {
		f, err := os.Open(*start)
		check(err)
		s, err := personal.LoadStore(f)
		f.Close()
		check(err)
		e.Model.Store = s
	}
	if *habitLift >= 0 {
		e.Model.P.HabitLift = *habitLift
	}
	const day = 86400
	now := e.Model.Store.Time
	if !e.Model.Store.TimeSet {
		now = 1790000000
	}
	e.Model.AdvanceTime(now)
	u := &user{e: e, id: e.OpenSession(), reader: eval.NewReader(e.Model.Lex.(*lexicon.Compact), e.Model.LM)}
	if *history != "" {
		hist, err := readCases(*history)
		check(err)
		rng := rand.New(rand.NewSource(*seed))
		for i, h := range hist {
			if *perDay > 0 && i > 0 && i%*perDay == 0 {
				now += day
				e.Model.AdvanceTime(now)
			}
			u.write(h.Text, rng.Float64() < *miss)
		}
		fmt.Printf("歷史：%d 句、%d 音節；打完就對 %d 句（%.2f%%）；手選 %d 次（每百音節 %.2f）；沒注意送出 %d 句；改不好 %d 句\n",
			u.sentences, u.typed, u.clean, 100*float64(u.clean)/float64(max(1, u.sentences)), u.picks,
			100*float64(u.picks)/float64(max(1, u.typed)), u.missed, u.unfixed)
	}
	store := e.Model.Store
	if *saveUser != "" {
		f, err := os.OpenFile(*saveUser, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		check(err)
		check(store.Clone().Save(f))
		check(f.Close())
	}
	if *casesFile == "" {
		return
	}
	cases, err := readCases(*casesFile)
	check(err)
	results := make([]*result, len(cases))
	next := make(chan int)
	var wg sync.WaitGroup
	for range max(1, *workers) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x, err := datadir.Open(*data)
			check(err)
			x.Model.Store = store.Clone()
			x.Model.P = e.Model.P
			reader := eval.NewReader(x.Model.Lex.(*lexicon.Compact), x.Model.LM)
			t := eval.NewTypist(x)
			for i := range next {
				syls, ok := reader.Annotate(cases[i].Text)
				if !ok {
					continue
				}
				got, _, _ := t.Replay(cases[i].Text, syls)
				results[i] = &result{cases[i].Cat, cases[i].Text, got}
			}
		}()
	}
	for i := range cases {
		next <- i
	}
	close(next)
	wg.Wait()
	type agg struct{ n, exact int }
	by := map[string]*agg{}
	var w *bufio.Writer
	if *details != "" {
		f, err := os.OpenFile(*details, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		check(err)
		defer f.Close()
		w = bufio.NewWriter(f)
		defer w.Flush()
	}
	for _, r := range results {
		if r == nil {
			continue
		}
		a := by[r.Cat]
		if a == nil {
			a = &agg{}
			by[r.Cat] = a
		}
		a.n++
		if r.Got == r.Want {
			a.exact++
		}
		if w != nil {
			b, _ := json.Marshal(r)
			w.Write(append(b, '\n'))
		}
	}
	cats := make([]string, 0, len(by))
	for c := range by {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		a := by[c]
		fmt.Printf("%-16s %6d 句  整句正確 %6d（%.2f%%）\n", c, a.n, a.exact, 100*float64(a.exact)/float64(a.n))
	}
}
