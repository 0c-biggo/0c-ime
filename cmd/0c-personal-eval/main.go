// 0c-personal-eval 載入一份個人資料（user.json 副本），逐音節打一組文字並統計整段正確率與字錯率，
// 用來比較個人化參數對「使用者之後寫的文字」與一般文字的影響。只讀個人檔、不學習、不寫回。
//
// 案例來源二選一或並用：
//   - -cases：TSV，每行「類別<TAB>文字」。
//   - -future 舊檔,新檔：取新檔裡在舊檔之後更新過的詞對（EvidenceByContext），句首詞為 future-start，
//     前後兩詞為 future-pair；用舊檔當個人資料時即為「預測之後寫的文字」的保留測試。
//
// -advance-days 把個人資料的時間往後推（照常衰減），-prune 再套用清理（issue #129），
// 用來比較同一時點清理前後的結果；-stats 印出各類資料筆數與存檔大小（不含文字）。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

type kase struct{ Cat, Text string }

type result struct {
	Cat  string `json:"cat"`
	Want string `json:"want"`
	Got  string `json:"got"`
	Dist int    `json:"dist"`
	// Tab 補全（只看建議、不接受）：顯示次數、其中正確的次數、接受正確建議可省的按鍵與總按鍵。
	Shown   int `json:"shown"`
	Correct int `json:"correct"`
	Saved   int `json:"saved"`
	Keys    int `json:"keys"`
}

func loadStore(path string) (*personal.Store, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return personal.LoadStore(f)
}

func han(s string) bool {
	for _, r := range s {
		if !zhuyin.IsHan(r) {
			return false
		}
	}
	return s != ""
}

func futureCases(oldPath, newPath string) ([]kase, error) {
	old, err := loadStore(oldPath)
	if err != nil {
		return nil, err
	}
	cur, err := loadStore(newPath)
	if err != nil {
		return nil, err
	}
	var out []kase
	for k, e := range cur.EvidenceByContext {
		left, w, _ := strings.Cut(k, "\t")
		if e.Updated <= old.Time || !han(w) {
			continue
		}
		switch {
		case left == lm.BOS:
			out = append(out, kase{"future-start", w})
		case han(left):
			out = append(out, kase{"future-pair", left + w})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cat+out[i].Text < out[j].Cat+out[j].Text })
	return out, nil
}

func readCases(path string) ([]kase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []kase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if cat, text, ok := strings.Cut(sc.Text(), "\t"); ok && text != "" {
			out = append(out, kase{cat, text})
		}
	}
	return out, sc.Err()
}

func main() {
	data := flag.String("data", "build/data", "資料包目錄")
	user := flag.String("user", "", "個人資料檔（唯讀）；空白為不帶個人資料")
	casesFile := flag.String("cases", "", "案例 TSV：類別<TAB>文字")
	future := flag.String("future", "", "舊個人檔,新個人檔：取新檔在舊檔之後更新的詞對當案例")
	typePrior := flag.Float64("type-prior", -1, "覆寫 personal.Params.TypePrior；負值為預設")
	workers := flag.Int("workers", 8, "平行引擎數")
	details := flag.String("details", "", "每個案例的結果 JSONL（不覆蓋既有檔案；含個人文字，只留本機）")
	advanceDays := flag.Float64("advance-days", 0, "評測前把個人資料時間往後推幾天（照常衰減）")
	prune := flag.Bool("prune", false, "評測前套用個人資料清理（在 -advance-days 之後）")
	pruneShare := flag.Float64("prune-share", -1, "覆寫 PrunePolicy.Share；負值為預設")
	pruneMargin := flag.Float64("prune-margin", -1, "覆寫 PrunePolicy.Margin；負值為預設")
	pruneMaxPairs := flag.Int("prune-max-pairs", -1, "覆寫 PrunePolicy.MaxPairs；負值為預設")
	pruneMaxEvidence := flag.Int("prune-max-evidence", -1, "覆寫 PrunePolicy.MaxEvidence；負值為預設")
	pruneEvidence := flag.Float64("prune-evidence", -1, "覆寫 PrunePolicy.Evidence；負值為預設")
	stats := flag.Bool("stats", false, "印出個人資料各類筆數與存檔大小（不含文字）")
	flag.Parse()
	check := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	var cases []kase
	if *casesFile != "" {
		c, err := readCases(*casesFile)
		check(err)
		cases = append(cases, c...)
	}
	if *future != "" {
		oldPath, newPath, ok := strings.Cut(*future, ",")
		if !ok {
			check(fmt.Errorf("-future 需要「舊檔,新檔」"))
		}
		c, err := futureCases(oldPath, newPath)
		check(err)
		cases = append(cases, c...)
	}
	if len(cases) == 0 && !*stats {
		check(fmt.Errorf("沒有案例：指定 -cases 或 -future"))
	}
	var base *personal.Store
	if *user != "" {
		e, err := datadir.Open(*data)
		check(err)
		base, err = loadStore(*user)
		check(err)
		e.Model.Store = base
		if *advanceDays > 0 {
			e.Model.AdvanceTime(base.Time + int64(*advanceDays*86400))
		}
		if *prune {
			policy := personal.DefaultPrunePolicy()
			for _, o := range []struct {
				v   float64
				dst *float64
			}{{*pruneShare, &policy.Share}, {*pruneMargin, &policy.Margin}, {*pruneEvidence, &policy.Evidence}} {
				if o.v >= 0 {
					*o.dst = o.v
				}
			}
			if *pruneMaxPairs >= 0 {
				policy.MaxPairs = *pruneMaxPairs
			}
			if *pruneMaxEvidence >= 0 {
				policy.MaxEvidence = *pruneMaxEvidence
			}
			started := time.Now()
			pruned := e.Model.PruneWith(policy)
			fmt.Printf("清理：%+v（%v）\n", pruned, time.Since(started).Round(time.Millisecond))
		}
		if *stats {
			printStats(base)
		}
	}
	if len(cases) == 0 {
		return
	}
	results := make([]*result, len(cases))
	n := max(1, *workers)
	next := make(chan int)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := datadir.Open(*data)
			if err == nil && base != nil {
				e.Model.Store = base.Clone()
			}
			if err != nil {
				errs <- err
				for range next {
				}
				return
			}
			if *typePrior >= 0 {
				e.Model.P.TypePrior = *typePrior
			}
			reader := eval.NewReader(e.Model.Lex.(*lexicon.Compact), e.Model.LM)
			t := eval.NewTypist(e) // 暫停學習：提交不改變之後的案例
			for i := range next {
				syls, ok := reader.Annotate(cases[i].Text)
				if !ok {
					continue
				}
				tab := t.Tab
				got, _, _ := t.Replay(cases[i].Text, syls)
				results[i] = &result{cases[i].Cat, cases[i].Text, got, eval.EditDistance(cases[i].Text, got),
					t.Tab.Shown - tab.Shown, t.Tab.Correct - tab.Correct, t.Tab.SavedKeys - tab.SavedKeys, t.Tab.Keys - tab.Keys}
			}
		}()
	}
	for i := range cases {
		next <- i
	}
	close(next)
	wg.Wait()
	close(errs)
	for err := range errs {
		check(err)
	}

	type agg struct{ n, exact, dist, chars, shown, correct, saved, keys int }
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
		a.dist += r.Dist
		a.chars += len([]rune(r.Want))
		a.shown += r.Shown
		a.correct += r.Correct
		a.saved += r.Saved
		a.keys += r.Keys
		if r.Dist == 0 {
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
		fmt.Printf("%-14s n=%5d 整段正確=%5d（%.2f%%） CER=%.2f%%  Tab 建議=%d 正確=%d 省鍵=%.2f%%\n", c, a.n, a.exact, 100*float64(a.exact)/float64(a.n), 100*float64(a.dist)/float64(a.chars),
			a.shown, a.correct, 100*float64(a.saved)/float64(max(1, a.keys)))
	}
}

// printStats 只印筆數與位元組數，不印鍵值。
func printStats(s *personal.Store) {
	var total countingWriter
	check := s.Save(&total)
	if check != nil {
		fmt.Fprintln(os.Stderr, check)
		os.Exit(1)
	}
	fields := []struct {
		name string
		n    int
		v    any
	}{
		{"Bi", len(s.Bi), s.Bi}, {"Ctx", len(s.Ctx), s.Ctx}, {"Uni", len(s.Uni), s.Uni}, {"Uses", len(s.Uses), s.Uses},
		{"EvidenceByContext", len(s.EvidenceByContext), s.EvidenceByContext}, {"EvidenceByWord", len(s.EvidenceByWord), s.EvidenceByWord},
		{"Corrections", len(s.Corrections), s.Corrections}, {"PhraseSeen", len(s.PhraseSeen), s.PhraseSeen}, {"Phrases", len(s.Phrases), s.Phrases},
		{"Readings", len(s.Readings), s.Readings}, {"Picked", len(s.Picked), s.Picked}, {"Tombstones", len(s.Tombstones), s.Tombstones},
		{"Rejects", len(s.Rejects), s.Rejects},
	}
	for _, f := range fields {
		var w countingWriter
		json.NewEncoder(&w).Encode(f.v)
		fmt.Printf("%-18s n=%7d bytes=%9d\n", f.name, f.n, int64(w))
	}
	fmt.Printf("%-18s bytes=%9d\n", "user.json", int64(total))
}

type countingWriter int64

func (w *countingWriter) Write(p []byte) (int, error) {
	*w += countingWriter(len(p))
	return len(p), nil
}

var _ io.Writer = (*countingWriter)(nil)
