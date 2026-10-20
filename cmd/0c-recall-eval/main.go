// 0c-recall-eval 只在離線量測整段候選召回，不載入個人檔、不訓練模型。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

type sample struct {
	ID      string            `json:"id"`
	Want    string            `json:"want"`
	Reading []zhuyin.Syllable `json:"reading"`
}

var forms = strings.NewReplacer("臺", "台", "裏", "裡", "峯", "峰", "綫", "線")

func rank(rows []decode.Hypothesis, text string, fold bool) int {
	if fold {
		text = forms.Replace(text)
	}
	for i, h := range rows {
		got := h.Text
		if fold {
			got = forms.Replace(got)
		}
		if got == text {
			return i + 1
		}
	}
	return 0
}

func shown(path []decode.Seg) string {
	var b strings.Builder
	for _, seg := range path {
		b.WriteString(seg.Word)
	}
	return b.String()
}

func boundaries(path []decode.Seg) []int {
	var b []int
	for _, seg := range path {
		b = append(b, seg.End)
	}
	return b
}

type caseResult struct {
	sample
	Production          string              `json:"production"`
	ProductionScore     *float64            `json:"production_score,omitempty"`
	Rank                int                 `json:"rank"`
	FoldedRank          int                 `json:"folded_rank"`
	Layer               string              `json:"layer"`
	UnprunedRank        int                 `json:"unpruned_rank_when_32_missed,omitempty"`
	Target              decode.TargetAudit  `json:"target"`
	DifferentBoundaries bool                `json:"different_boundaries"`
	Top1Disagrees       bool                `json:"nbest_top1_disagrees"`
	Top1Tie             bool                `json:"nbest_top1_tie"`
	Candidates          []decode.Hypothesis `json:"candidates"`
	Stats               decode.SearchStats  `json:"stats"`
}

func layer(correct bool, r int, target decode.TargetAudit, recovered int) string {
	if correct {
		return "production_correct"
	}
	if !target.ReadingReachable {
		if !target.SurfaceSegmentable {
			return "surface_unreachable"
		}
		return "reading_or_lock_unreachable"
	}
	if target.Best == nil {
		return "prefix_pool_or_zero_score"
	}
	if r > 0 {
		return "ranking_within_32"
	}
	if recovered > 0 {
		return "beam_or_prefix_context_pruned"
	}
	return "outside_unpruned_top32"
}

func distribution(ds []time.Duration) map[string]int64 {
	if len(ds) == 0 {
		return nil
	}
	copyDS := append([]time.Duration(nil), ds...)
	sort.Slice(copyDS, func(i, j int) bool { return copyDS[i] < copyDS[j] })
	out := map[string]int64{}
	for _, p := range []int{50, 95, 99, 100} {
		out[fmt.Sprintf("p%d_us", p)] = copyDS[(len(copyDS)-1)*p/100].Microseconds()
	}
	return out
}

func main() {
	data := flag.String("data", "", "受測配對資料包（必填）")
	reference := flag.String("reference-data", "build/data", "固定參考標音資料包")
	corpus := flag.String("sentences", "data/raw/cv/*.txt", "語料 glob")
	split := flag.String("split", "dev", "dev/test/all；E0 使用 dev")
	cases := flag.String("cases", "", "明確注音案例 TSV：id/want/reading/...；設定後不讀語料")
	limit := flag.Int("limit", 100000, "最多可標音段數")
	bench := flag.Int("benchmark", 0, "前幾段另量 K8/16/32 冷／暖快取；不改候選參數")
	report := flag.String("report", "", "統計 JSON（必填，不覆蓋）")
	detail := flag.String("details", "", "每段候選 JSONL（必填，不覆蓋；含公開評測文字）")
	flag.Parse()
	fail := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *data == "" || *report == "" || *detail == "" || *report == *detail || *limit < 1 || *bench < 0 || !slices.Contains([]string{"dev", "test", "all"}, *split) {
		fail(fmt.Errorf("invalid flags"))
	}
	for _, path := range []string{*report, *detail} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			fail(fmt.Errorf("output already exists or inaccessible: %s", path))
		}
	}
	started := time.Now()
	eng, err := datadir.Open(*data)
	fail(err)
	ref, err := datadir.Open(*reference)
	fail(err)
	reader := eval.NewReader(ref.Model.Lex.(*lexicon.Compact), ref.Model.LM)
	var samples []sample
	unannotated := 0
	sources := map[string]string{}
	if *cases != "" {
		b, err := os.ReadFile(*cases)
		fail(err)
		sources[*cases] = fmt.Sprintf("%x", sha256.Sum256(b))
		r := csv.NewReader(strings.NewReader(string(b)))
		r.Comma = '\t'
		rows, err := r.ReadAll()
		fail(err)
		if len(rows) == 0 || len(rows[0]) < 3 {
			fail(fmt.Errorf("missing case header"))
		}
		for _, row := range rows[1:] {
			ys := zhuyin.ParseReading(row[2])
			if len([]rune(row[1])) != len(ys) {
				fail(fmt.Errorf("case %s: text/reading length mismatch", row[0]))
			}
			samples = append(samples, sample{row[0], row[1], ys})
		}
	} else {
		files, err := filepath.Glob(*corpus)
		fail(err)
		if len(files) == 0 {
			fail(fmt.Errorf("no corpus files"))
		}
		var chunks []string
		for _, name := range files {
			b, err := os.ReadFile(name)
			fail(err)
			sources[name] = fmt.Sprintf("%x", sha256.Sum256(b))
			sc := bufio.NewScanner(strings.NewReader(string(b)))
			for sc.Scan() {
				chunks = append(chunks, eval.Chunks(sc.Text(), 2)...)
			}
			fail(sc.Err())
		}
		sort.Strings(chunks)
		chunks = eval.Split(slices.Compact(chunks), *split)
		rand.New(rand.NewSource(1)).Shuffle(len(chunks), func(i, j int) { chunks[i], chunks[j] = chunks[j], chunks[i] })
		for _, text := range chunks {
			if len(samples) >= *limit {
				break
			}
			ys, ok := reader.Annotate(text)
			if !ok {
				unannotated++
				continue
			}
			samples = append(samples, sample{fmt.Sprintf("%06d", len(samples)), text, ys})
		}
	}
	samples = samples[:min(*limit, len(samples))]
	if len(samples) == 0 {
		fail(fmt.Errorf("no annotated samples"))
	}
	loaded := time.Since(started)
	file, err := os.OpenFile(*detail, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	fail(err)
	defer file.Close()
	enc := json.NewEncoder(file)
	opt := session.DefaultOptions()
	newDecoder := func() *decode.Decoder {
		m := personal.NewModel(eng.Model.Lex, eng.Model.LM)
		return &decode.Decoder{Lex: m.Source(), Score: m, Beam: opt.Beam, Continue: opt.Continue, SeparateContinuation: opt.SeparateContinuation}
	}
	d, production := newDecoder(), newDecoder()
	counts, layers, timings := map[string]int{}, map[string]int{}, map[string][]time.Duration{}
	allocs := map[string]uint64{}
	inputHash := sha256.New()
	stats := decode.SearchStats{}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i, s := range samples {
		encoded, _ := json.Marshal(s)
		inputHash.Write(encoded)
		inputHash.Write([]byte{'\n'})
		lat := input.Exact{}.Expand(s.Reading)
		t0 := time.Now()
		path, sc := production.Best(lat, nil, opt.PrefixAware)
		timings["production_best"] = append(timings["production_best"], time.Since(t0))
		t0 = time.Now()
		hyp, st := d.NBest(lat, nil, opt.PrefixAware, 32)
		timings["nbest32"] = append(timings["nbest32"], time.Since(t0))
		r := caseResult{sample: s, Production: shown(path), Rank: rank(hyp, s.Want, false), FoldedRank: rank(hyp, s.Want, true), Candidates: hyp, Stats: st}
		if !math.IsInf(sc, 0) && !math.IsNaN(sc) {
			r.ProductionScore = &sc
		}
		if len(hyp) > 0 && hyp[0].Text != r.Production {
			r.Top1Disagrees = true
			r.Top1Tie = math.Abs(sc-hyp[0].LogScore) < 1e-9
			counts["top1_disagreements"]++
			if r.Top1Tie {
				counts["top1_disagreements_tied"]++
			}
		}
		r.Target = d.AuditTarget(lat, nil, opt.PrefixAware, s.Want)
		if r.Rank == 0 && r.Target.Best != nil {
			unpruned := newDecoder()
			unpruned.Beam, unpruned.TopB = 0, math.MaxInt
			t0 = time.Now()
			full, _ := unpruned.NBest(lat, nil, opt.PrefixAware, 32)
			timings["unpruned32_on_misses"] = append(timings["unpruned32_on_misses"], time.Since(t0))
			r.UnprunedRank = rank(full, s.Want, false)
			if r.UnprunedRank == 0 && (len(full) < 32 || r.Target.Best.LogScore > full[len(full)-1].LogScore+1e-9) {
				fail(fmt.Errorf("oracle exceeds N-best cutoff but missing: %s", s.ID))
			}
		}
		r.Layer = layer(s.Want == r.Production, r.Rank, r.Target, r.UnprunedRank)
		layers[r.Layer]++
		counts["cases"]++
		counts["characters"] += len([]rune(s.Want))
		if s.Want == r.Production {
			counts["production_correct"]++
		}
		if forms.Replace(s.Want) == forms.Replace(r.Production) {
			counts["production_folded_correct"]++
		}
		if r.Target.ReadingReachable {
			counts["reading_reachable"]++
		}
		if r.Target.Best != nil {
			counts["capped_graph_reachable"]++
			r.DifferentBoundaries = !reflect.DeepEqual(boundaries(path), boundaries(r.Target.Best.Segments))
			if r.Production != s.Want && r.DifferentBoundaries {
				counts["production_errors_with_different_target_boundaries"]++
			}
		}
		for _, k := range []int{1, 8, 16, 32} {
			if r.Rank > 0 && r.Rank <= k {
				counts[fmt.Sprintf("recall_at_%d", k)]++
			}
			if r.FoldedRank > 0 && r.FoldedRank <= k {
				counts[fmt.Sprintf("folded_recall_at_%d", k)]++
			}
		}
		stats.ScoredExtensions += st.ScoredExtensions
		stats.RetainedPrefixes = max(stats.RetainedPrefixes, st.RetainedPrefixes)
		stats.PrunedContexts += st.PrunedContexts
		fail(enc.Encode(r))
		if i < *bench {
			for _, k := range []int{8, 16, 32} {
				benchD := newDecoder()
				for _, mode := range []string{"cold", "warm"} {
					name := fmt.Sprintf("k%d_%s", k, mode)
					var a, b runtime.MemStats
					runtime.ReadMemStats(&a)
					t0 := time.Now()
					rows, _ := benchD.NBest(lat, nil, opt.PrefixAware, k)
					timings[name] = append(timings[name], time.Since(t0))
					runtime.ReadMemStats(&b)
					allocs[name] += b.TotalAlloc - a.TotalAlloc
					if !reflect.DeepEqual(rows, hyp[:min(k, len(hyp))]) {
						fail(fmt.Errorf("K=%d not nested on %s", k, s.ID))
					}
				}
			}
		}
		if (i+1)%100 == 0 {
			fmt.Fprintf(os.Stderr, "%d/%d cases\n", i+1, len(samples))
		}
	}
	runtime.ReadMemStats(&after)
	latency := map[string]any{}
	for name, ds := range timings {
		item := map[string]any{"samples": len(ds), "quantiles": distribution(ds)}
		if bytes, measured := allocs[name]; measured {
			item["total_allocated_bytes"] = bytes
		}
		latency[name] = item
	}
	fail(file.Close())
	result := map[string]any{"schema": 1, "data": eval.Provenance(*data), "reference": eval.ProvenanceData(*reference, *reference), "sources": sources, "split": *split, "input_sha256": hex.EncodeToString(inputHash.Sum(nil)), "seed": 1, "counts": counts, "error_layers": layers, "latency": latency, "search_stats_sum_except_max_retained_prefixes": stats, "load_and_annotate_ms": loaded.Milliseconds(), "elapsed_ms": time.Since(started).Milliseconds(), "heap_live_bytes_at_end": after.HeapAlloc, "total_allocated_bytes_including_diagnostics": after.TotalAlloc - before.TotalAlloc, "personal_loaded": false, "beam": opt.Beam, "continue": opt.Continue, "prefix_top_m": 64, "prefix_contexts": 4,
		"limits": []string{"Offline composition-only, exact syllables, empty personal store; no neural reranking, no daily accuracy claim.", "Auto-annotation cannot establish general real-world reading coverage; unannotated inputs reported separately.", "Distinct strings ranked by best path, not probability summed over segmentations; folded recall uses four existing form pairs.", "Unpruned32 is computed only on production-beam32 misses; recovery count is conditional, not global unpruned recall.", "Outside unpruned top32 means ranking/candidate-budget limitation, not proven absence or a neural model's achievable gain.", "TopM64 prefix pool remains fixed; unpruned diagnostic removes Beam and TopB only.", "Cold/warm benchmark means decoder-prefix cache only, not process/model cold start. Timing is offline whole-composition search, not per-key UI latency.", "Different boundaries use the best target-constrained segmentation, not human word-boundary labels."}, "unannotated_skipped": unannotated}
	fail(eval.WriteReport(*report, result))
	fmt.Printf("cases=%d recall@8=%d @16=%d @32=%d; layers=%v\n", counts["cases"], counts["recall_at_8"], counts["recall_at_16"], counts["recall_at_32"], layers)
}
