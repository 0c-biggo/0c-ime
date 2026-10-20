// 0c-context-eval 重播人工固定注音的上下文案例；不讀個人資料、不改參考輸入。
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

func main() {
	data := flag.String("data", "build/data", "編譯資料包")
	cases := flag.String("cases", "testdata/cases/context_cases.tsv", "人工固定注音的案例")
	explain := flag.Bool("explain", false, "列出首選與目標鎖定路徑的語言／讀音／前綴分數")
	sequences := flag.String("sequences", "", "重播更正操作案例 JSON（按詞面選取，不依候選序號；不讀寫個人檔）")
	flag.Parse()
	check := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *sequences != "" {
		check(runSequences(*data, *sequences))
		return
	}
	f, err := os.Open(*cases)
	check(err)
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	rows, err := r.ReadAll()
	check(err)
	e, err := datadir.Open(*data)
	check(err)
	id := e.OpenSession()
	provenance := eval.Provenance(*data)
	for _, row := range rows[1:] {
		if len(row) != 7 {
			check(fmt.Errorf("案例需要七欄：%v", row))
		}
		start, err := strconv.Atoi(row[3])
		check(err)
		length, err := strconv.Atoi(row[4])
		check(err)
		syls := zhuyin.ParseReading(row[2])
		if start < 0 || length <= 0 || start+length > len(syls) || len([]rune(row[5])) != length {
			check(fmt.Errorf("%s: 目標區間或字數不合法", row[0]))
		}
		e.Session(id).Handle(proto.Request{Type: proto.Cancel})
		e.Session(id).Handle(proto.Request{Type: proto.Pause, On: true})
		var snap proto.Snapshot
		keys, rewrites := 0, 0
		previous := []rune{}
		var rewriteStats eval.RewriteStats
		for syllableIndex, syl := range syls {
			if !syl.Legal() {
				check(fmt.Errorf("%s: 非法注音 %s", row[0], syl))
			}
			for _, k := range layout.Standard.Keys(syl) {
				snap = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: string(k)})
				keys++
			}
			cur := []rune(snap.Text())
			rewriteStats.Observe(string(previous), string(cur), row[1], syllableIndex+1)
			for i := 0; i < len(previous) && i < len(cur); i++ {
				if previous[i] != cur[i] {
					rewrites++
				}
			}
			previous = cur
		}
		shown, suggestion := snap.Text(), snap.Suggestion
		lat := input.Exact{}.Expand(syls)
		d := &decode.Decoder{Lex: e.Model.Source(), Score: e.Model, Beam: 8, Continue: 0.5}
		d.SeparateContinuation = e.Session(id).Opt.SeparateContinuation
		path := d.Decode(lat, nil, true)
		left := lm.BOS
		for _, seg := range path {
			if seg.End == start {
				left = seg.Word
			}
		}
		rank := func(cs []decode.Candidate) int {
			for i, c := range cs {
				if c.Entry.Surface == row[5] {
					return i + 1
				}
			}
			return 0
		}
		old := d.Candidates(lat, start, length, left, nil)
		homophone, hasHomophone := d.CompleteHomophones(lat, start, left)
		started := time.Now()
		context := d.CandidatesInContext(lat, start, length, nil, nil)
		elapsed := time.Since(started).Microseconds()
		var times []int64
		for range 30 {
			started := time.Now()
			d.CandidatesInContext(lat, start, length, nil, nil)
			times = append(times, time.Since(started).Microseconds())
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		_, bestScore := d.Best(lat, nil, false)
		unbounded := *d
		unbounded.Beam = 0
		unboundedText := ""
		for _, seg := range unbounded.Decode(lat, nil, false) {
			unboundedText += seg.Word
		}
		forced, forcedScore := d.Best(lat, map[int]decode.Lock{start: {Word: row[5], Len: length}}, false)
		forcedText := ""
		for _, seg := range forced {
			forcedText += seg.Word
		}
		var scoreDelta *float64
		if !math.IsInf(forcedScore, 0) && !math.IsInf(bestScore, 0) {
			v := forcedScore - bestScore
			scoreDelta = &v
		}
		var top []string
		for i, c := range context {
			if i == 5 {
				break
			}
			top = append(top, c.Entry.Surface)
		}
		accepted := ""
		preview := ""
		tabKeys := 0
		if row[6] != "" {
			after := e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyTab})
			if after.Handled {
				tabKeys++
				if after.SuggestionEdit != nil {
					preview = after.Suggestion
					after = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyTab})
					tabKeys++
				}
				accepted = after.Text()
			}
		}
		// 手動修正以真正的方向鍵／候選反白／Enter 重播，不把 cursor API 當一鍵。
		manualKeys, corrected := 0, ""
		if row[6] == "" && shown != row[1] {
			for range len(syls) - start {
				snap = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyLeft})
				manualKeys++
			}
			snap = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyDown})
			manualKeys++
			// 候選內 Tab 切換詞長；避免為了兩字詞走過整份單字清單。
			for attempts := 0; snap.Candidates != nil && snap.Candidates.Length != length && attempts < decode.MaxWordLen; attempts++ {
				snap = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyTab})
				manualKeys++
			}
			seen := map[string]bool{}
			for snap.Candidates != nil {
				c := snap.Candidates
				state := fmt.Sprintf("%d/%d/%d", c.Length, c.Page, c.Highlight)
				if seen[state] {
					break
				}
				seen[state] = true
				if c.Items[c.Highlight].Text == row[5] {
					snap = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter})
					manualKeys++
					corrected = snap.Text()
					break
				}
				snap = e.Session(id).Handle(proto.Request{Type: proto.Key, Key: proto.KeyDown})
				manualKeys++
			}
		}
		result := map[string]any{
			"runtime": provenance, "personal_loaded": false, "rewrite_stats": rewriteStats,
			"id": row[0], "want": row[1], "reading": row[2], "shown": shown, "whole_correct": shown == row[1],
			"typing_keys": keys, "rewritten_characters": rewrites, "target": row[5],
			"left_only_rank": rank(old), "context_rank": rank(context), "context_top5": top, "candidate_us": elapsed,
			"suggestion": suggestion, "accepted": accepted, "expected_completion": row[6],
			"homophone_best": homophone.Entry.Surface, "homophone_conf": homophone.Conf, "has_homophone": hasHomophone,
			"requested_preview": preview, "tab_keys": tabKeys,
			"candidate_p95_us_30": times[28], "candidate_p99_us_30": times[29],
			"forced_target_text": forcedText, "target_score_delta": scoreDelta,
			"unbounded_decode":                        unboundedText,
			"manual_correction_keys_excluding_commit": manualKeys, "corrected_text": corrected,
		}
		if *explain {
			result["best_path"] = d.ExplainBest(lat, nil, true)
			result["target_path"] = d.ExplainBest(lat, map[int]decode.Lock{start: {Word: row[5], Len: length}}, true)
			result["without_prefix"] = d.ExplainBest(lat, nil, false)
		}
		check(json.NewEncoder(os.Stdout).Encode(result))
	}
}
