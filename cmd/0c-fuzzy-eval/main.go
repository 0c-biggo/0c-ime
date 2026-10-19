// 0c-fuzzy-eval 量測模糊音（issue #45／#48／#49）：僅供選字的候選可達、auto 模式的自動修正與乾淨輸入誤改、延遲。
//
// 句子由 -sentences 指定（文字檔，每行一段文字；純漢字片段才會被取用，注音以資料包詞庫自動標示），
// 錯音是在參考注音上以固定種子換成近音的合成資料，比例由 -rate 決定。報告要與真人資料分開，
// 合成的總準確率不能決定預設值；-split 只是同一批資料內的雜湊切分，不等於「來源文件分組」
// ——要宣稱未見資料，必須由不同來源檔案提供（見 docs/47）。
package main

import (
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.biggo.com/0c/ime/core/engine"
	"git.biggo.com/0c/ime/core/fuzzy"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/eval"
	"git.biggo.com/0c/ime/internal/fuzzyeval"
	"git.biggo.com/0c/ime/internal/neural"
)

func main() {
	data := flag.String("data", "build/data", "資料包目錄")
	sentences := flag.String("sentences", "", "句子檔（glob，每行一段文字）")
	limit := flag.Int("limit", 2000, "評測句數上限（隨機抽樣，seed 固定）")
	minLen := flag.Int("min-len", 4, "句子最短字數")
	maxLen := flag.Int("max-len", 24, "句子最長字數；更長的片段略過")
	split := flag.String("split", "all", "dev（約 1/5，調門檻用）、test（其餘）、all；以片段內容雜湊切分")
	seed := flag.Int64("seed", 45, "抽樣與錯音的種子")
	rate := flag.Float64("rate", 0.3, "錯音比例：每個可換音音節被換成近音的機率")
	pairs := flag.String("pairs", "z-zh,c-ch,s-sh,en-eng", "啟用 auto 的配對")
	margin := flag.Float64("margin", -1, "自動修正基本門檻（預設用 fuzzy.DefaultAutoPolicy）")
	short := flag.Float64("short", -1, "只改一個音節時額外加的門檻")
	extra := flag.Float64("extra", -1, "每多換一個音節額外加的門檻")
	neuralExtra := flag.Float64("neural-extra", -1, "神經改過的處額外加的門檻")
	sweepNeural := flag.String("sweep-neural-extra", "", "逗號分隔的神經額外門檻清單（可用 1e9 表示神經改過的處一律不改）；對每個值各跑一次並列表，基準只跑一次")
	sweep := flag.String("sweep", "", "逗號分隔的基本門檻清單；對每個值各跑一次並列表（其餘照 -short／-extra）")
	workers := flag.Int("workers", 8, "平行執行緒數（每個各開一組引擎）")
	reportFile := flag.String("report", "", "輸出 JSON 報告")
	useNeural := flag.Bool("neural", false, "原版本與 auto 兩個角色接資料包旁內附的神經選字（需 neural/ 與配對字典）；僅供選字的可達量測不接")
	neuralTimeout := flag.Duration("neural-timeout", 100*time.Millisecond, "神經每次呼叫期限（出貨值，上限 100 ms）；平行負載太高會逾時退回統計，結尾會印逾時次數，過多時降低 -workers")
	neuralThreads := flag.Int("neural-threads", 2, "每個神經 worker 的 CPU threads")
	tagFiles := flag.Bool("tag-files", false, "依來源檔名分層（每個 -sentences 檔各一層）")
	rareUni := flag.Float64("rare-uni", 1e-7, "句中有詞的無上下文機率低於此值（或詞庫標為罕用）即歸入 rare 層；0 不分")
	changes := flag.String("changes", "", "把 auto 改過或改壞乾淨輸入的句子寫到檔案（TSV；最後一組門檻）")
	flag.Parse()
	if *sentences == "" {
		fatal(fmt.Errorf("需要 -sentences"))
	}

	var sups supervisors
	defer sups.close()
	open := func(arm fuzzyeval.Arm) (*engine.Engine, error) {
		e, err := datadir.Open(*data)
		if err != nil || !*useNeural || arm == fuzzyeval.ArmSelect {
			return e, err
		}
		return e, sups.attach(e, *data, *neuralTimeout, *neuralThreads)
	}
	eng, err := datadir.Open(*data)
	fatal(err)
	lex, ok := eng.Model.Lex.(*lexicon.Compact)
	if !ok {
		fatal(fmt.Errorf("評測需要編譯後的資料包"))
	}
	rd := eval.NewReader(lex, eng.Model.LM)

	var chunks []string
	sourceOf := map[string]string{}
	files, _ := filepath.Glob(*sentences)
	for _, p := range files {
		f, err := os.Open(p)
		fatal(err)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		for sc.Scan() {
			for _, c := range eval.Chunks(sc.Text(), *minLen) {
				if n := len([]rune(c)); n <= *maxLen {
					chunks = append(chunks, c)
					if _, seen := sourceOf[c]; !seen {
						sourceOf[c] = name
					}
				}
			}
		}
		f.Close()
	}
	sort.Strings(chunks)
	chunks = dedupe(chunks)
	chunks = eval.Split(chunks, *split)
	rand.New(rand.NewSource(*seed)).Shuffle(len(chunks), func(i, j int) { chunks[i], chunks[j] = chunks[j], chunks[i] })
	var cases []fuzzyeval.Case
	uncovered := 0
	for _, c := range chunks {
		if len(cases) >= *limit {
			break
		}
		reading, words, ok := rd.Segment(c)
		if !ok {
			uncovered++
			continue
		}
		kase := fuzzyeval.Case{Ref: c, Reading: reading, Words: words}
		if *tagFiles {
			kase.Tags = append(kase.Tags, sourceOf[c])
		}
		if *rareUni > 0 && hasRare(lex, eng.Model.LM, words, *rareUni) {
			kase.Tags = append(kase.Tags, "rare")
		}
		cases = append(cases, kase)
	}

	var auto fuzzy.Config
	for _, id := range strings.Split(*pairs, ",") {
		p, ok := fuzzy.ParsePair(strings.TrimSpace(id))
		if !ok {
			fatal(fmt.Errorf("未知的配對 %q", id))
		}
		auto = auto.With(p, fuzzy.Auto)
	}
	policy := fuzzy.DefaultAutoPolicy()
	if *margin >= 0 {
		policy.Margin = *margin
	}
	if *short >= 0 {
		policy.Short = *short
	}
	if *extra >= 0 {
		policy.PerExtra = *extra
	}
	if *neuralExtra >= 0 {
		policy.NeuralExtra = *neuralExtra
	}
	params := fuzzyeval.Params{Auto: auto, Policy: policy, Rate: *rate, Seed: *seed}
	fmt.Printf("資料包 %s；句子 %d（詞庫無法標音而略過 %d）；錯音比例 %.2f；配對 %s；split=%s seed=%d\n", *data, len(cases), uncovered, *rate, auto.Key(), *split, *seed)
	bases, err := fuzzyeval.RunBase(open, cases, params, *workers)
	fatal(err)

	type row struct {
		Policy fuzzy.AutoPolicy `json:"policy"`
		Report fuzzyeval.Report `json:"report"`
	}
	var rows []row
	var lastAutos []fuzzyeval.Auto
	policies := []fuzzy.AutoPolicy{policy}
	if *sweepNeural != "" {
		policies = nil
		for _, f := range strings.Split(*sweepNeural, ",") {
			x, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
			fatal(err)
			pp := policy
			pp.NeuralExtra = x
			policies = append(policies, pp)
		}
	}
	if *sweep != "" {
		policies = nil
		for _, f := range strings.Split(*sweep, ",") {
			m, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
			fatal(err)
			pp := policy
			pp.Margin = m
			policies = append(policies, pp)
		}
	}
	for _, pol := range policies {
		params.Policy = pol
		autos, err := fuzzyeval.RunAuto(open, bases, params, *workers)
		fatal(err)
		rows = append(rows, row{pol, fuzzyeval.Summarize(bases, autos)})
		lastAutos = autos
	}
	if *changes != "" {
		f, err := os.Create(*changes)
		fatal(err)
		w := bufio.NewWriter(f)
		fmt.Fprintln(w, "kind\tref\tnoisy_input\toff\tauto\twhy")
		for i, b := range bases {
			a := lastAutos[i]
			if a.Clean != b.Case.Ref && b.OffClean == b.Case.Ref {
				fmt.Fprintf(w, "clean-false-change\t%s\t%s\t%s\t%s\t%s\n", b.Case.Ref, "", b.OffClean, a.Clean, strings.Join(a.CleanWhy, " | "))
			}
			if len(b.Swapped) > 0 && a.Noisy != b.OffNoisy {
				kind := "changed"
				switch eo, ea := eval.EditDistance(b.OffNoisy, b.Case.Ref), eval.EditDistance(a.Noisy, b.Case.Ref); {
				case ea < eo:
					kind = "improved"
				case ea > eo:
					kind = "worsened"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", kind, b.Case.Ref, zhuyinKey(b.Noisy), b.OffNoisy, a.Noisy, strings.Join(a.NoisyWhy, " | "))
			}
		}
		fatal(w.Flush())
		fatal(f.Close())
	}
	if len(rows) == 1 {
		printOne(rows[0].Policy, rows[0].Report)
		if len(lastAutos) > 0 {
			printTags(fuzzyeval.SummarizeBy(bases, lastAutos))
		}
	} else {
		printSweep(rows[0].Report, func(yield func(fuzzy.AutoPolicy, fuzzyeval.Report)) {
			for _, r := range rows {
				yield(r.Policy, r.Report)
			}
		})
	}
	if *reportFile != "" {
		fatal(eval.WriteReport(*reportFile, map[string]any{
			"provenance": eval.Provenance(*data), "sentences": len(cases), "rate": *rate, "pairs": auto.Key(),
			"split": *split, "seed": *seed, "runs": rows,
		}))
	}
}

func printOne(pol fuzzy.AutoPolicy, r fuzzyeval.Report) {
	fmt.Printf("門檻 margin=%.1f short=+%.1f extra=+%.1f\n", pol.Margin, pol.Short, pol.PerExtra)
	if r.Excluded > 0 {
		fmt.Printf("排除 %d 句：打那句時神經評分器有按鍵沒評分（逾時、不可用），兩個角色不可比\n", r.Excluded)
	}
	c, n := r.Clean, r.Noisy
	fmt.Printf("\n乾淨輸入（參考注音）\n  可換音音節的句子 %d；其中原版本答錯 %d、答對 %d\n", c.EligibleTotal, c.BaseWrong, c.Applicable)
	fmt.Printf("  乾淨誤修正率 %d/%d = %.3f%%（字元編輯 %d／%d）\n", c.FalseChanges, c.Applicable, 100*c.FalseChangeRate, c.FalseChars, c.AppChars)
	fmt.Printf("\n錯音輸入（%d 句、%d 個換音音節）\n", n.Sentences, n.Swaps)
	fmt.Printf("  整句答對：原版本 %d → auto %d；CER %.2f%% → %.2f%%\n", n.OffExact, n.AutoExact, 100*n.OffCER, 100*n.AutoCER)
	fmt.Printf("  修復 %d（原版本錯 %d 句中 %.1f%%）、傷害 %d\n", n.Fixed, n.OffWrongSen, 100*n.RepairRate, n.Harmed)
	fmt.Printf("  auto 有改 %d 句：變好 %d、變差 %d（precision %.1f%%、coverage %.1f%%）\n", n.Changed, n.Improved, n.Worsened, 100*n.Precision, 100*n.Coverage)
	rc := r.Recall
	fmt.Printf("\n僅供選字：含換音音節的詞 %d 個；目標詞名次 ≤8 %.1f%%、≤10（第一頁）%.1f%%、≤16 %.1f%%、可達 %.1f%%（其中以近音候選到達 %d）\n", rc.Words, 100*rc.At8, 100*rc.AtPage1, 100*rc.At16, 100*rc.Any, rc.NearWords)
	fmt.Printf("逐鍵回改（乾淨輸入）：原版本 %d、auto %d\n", r.Rewrites.Off, r.Rewrites.Auto)
	l := r.LatencyMs
	fmt.Printf("每個請求延遲 ms（P50／P95／P99）：原版本 %.2f／%.2f／%.2f；auto %.2f／%.2f／%.2f；開近音候選 %.2f／%.2f／%.2f\n",
		l.Off.P50, l.Off.P95, l.Off.P99, l.Auto.P50, l.Auto.P95, l.Auto.P99, l.OpenNear.P50, l.OpenNear.P95, l.OpenNear.P99)
}

func printSweep(first fuzzyeval.Report, each func(func(fuzzy.AutoPolicy, fuzzyeval.Report))) {
	fmt.Printf("\n乾淨可誤改句 %d；錯音 %d 句（原版本錯 %d）\n", first.Clean.Applicable, first.Noisy.Sentences, first.Noisy.OffWrongSen)
	fmt.Printf("%7s %6s %6s %8s | %9s %9s | %8s %7s %8s %7s | %9s\n", "margin", "short", "extra", "神經額外", "乾淨誤改", "誤改句", "修復率", "傷害", "precision", "變動", "CER")
	each(func(p fuzzy.AutoPolicy, r fuzzyeval.Report) {
		fmt.Printf("%7.1f %6.1f %6.1f %8.4g | %8.3f%% %9d | %7.1f%% %7d %7.1f%% %7d | %4.1f→%4.1f%%\n", p.Margin, p.Short, p.PerExtra, p.NeuralExtra,
			100*r.Clean.FalseChangeRate, r.Clean.FalseChanges, 100*r.Noisy.RepairRate, r.Noisy.Harmed, 100*r.Noisy.Precision, r.Noisy.Changed,
			100*r.Noisy.OffCER, 100*r.Noisy.AutoCER)
	})
}

// hasRare 回報句中是否有罕用詞：詞庫標為罕用，或無上下文機率低於 cutoff。
func hasRare(lex *lexicon.Compact, sys interface{ Uni(string) float64 }, words []string, cutoff float64) bool {
	for _, w := range words {
		if sys.Uni(w) < cutoff {
			return true
		}
		for _, e := range lex.Lookup(w) {
			if e.Rare {
				return true
			}
		}
	}
	return false
}

func printTags(by map[string]fuzzyeval.Report) {
	if len(by) < 2 {
		return
	}
	tags := make([]string, 0, len(by))
	for t := range by {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	fmt.Printf("\n分層（標籤 | 乾淨誤改／可誤改 | 錯音句 修復率 傷害 precision | 僅供選字可達 ≤第一頁）\n")
	for _, t := range tags {
		r := by[t]
		name := t
		if name == "" {
			name = "(無標籤)"
		}
		fmt.Printf("  %-18s | %d/%d = %.3f%% | %d 句 %.1f%% %d %.1f%% | %.1f%%（%d 詞）\n", name, r.Clean.FalseChanges, r.Clean.Applicable, 100*r.Clean.FalseChangeRate,
			r.Noisy.Sentences, 100*r.Noisy.RepairRate, r.Noisy.Harmed, 100*r.Noisy.Precision, 100*r.Recall.AtPage1, r.Recall.Words)
	}
}

func zhuyinKey(s []zhuyin.Syllable) string { return zhuyin.Key(s) }

func dedupe(xs []string) []string {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "0c-fuzzy-eval:", err)
		os.Exit(1)
	}
}

// supervisors 追蹤評測開的神經 worker，結束時統一關閉並印出統計。
type supervisors struct {
	mu   sync.Mutex
	list []*neural.Supervisor
}

func (s *supervisors) attach(e *engine.Engine, data string, timeout time.Duration, threads int) error {
	b := neural.FindBundled(data)
	if !b.HasSelect {
		return fmt.Errorf("%s 旁沒有可用的 neural/select", data)
	}
	manifest, err := os.ReadFile(filepath.Join(data, "manifest.json"))
	if err != nil {
		return err
	}
	w, err := neural.OpenSupervised(neural.Config{Python: b.Python, Script: b.SelectWorker, Package: b.SelectPackage,
		DictionarySHA256: fmt.Sprintf("%x", sha256.Sum256(manifest)), Threads: threads, Timeout: timeout})
	if err != nil {
		return err
	}
	e.Rerank = w.Options()
	s.mu.Lock()
	s.list = append(s.list, w)
	s.mu.Unlock()
	return nil
}

func (s *supervisors) close() {
	var total neural.Stats
	for _, w := range s.list {
		st := w.Stats()
		total.Calls += st.Calls
		total.Scored += st.Scored
		total.Timeouts += st.Timeouts
		total.Errors += st.Errors
		total.Unavailable += st.Unavailable
		total.TotalUS += st.TotalUS
		w.Close()
	}
	if total.Calls > 0 {
		fmt.Printf("\n神經：呼叫 %d、評分 %d、逾時 %d、錯誤 %d、不可用 %d；平均每次 %.1f ms\n", total.Calls, total.Scored, total.Timeouts, total.Errors, total.Unavailable, float64(total.TotalUS)/float64(total.Calls)/1000)
	}
}
