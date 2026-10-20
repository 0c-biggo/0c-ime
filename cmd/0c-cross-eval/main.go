// 0c-cross-eval 固定標音、提交切點及詞庫，單獨比較跨提交前文的混合權重。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

type result struct {
	Weight                 float64  `json:"weight"`
	Cases                  int      `json:"cases"`
	Tokens                 int      `json:"token_available"`
	LeftCorrect            int      `json:"left_correct"`
	Correct                int      `json:"suffix_correct"`
	Edits                  int      `json:"suffix_edits"`
	Characters             int      `json:"suffix_characters"`
	Fixed                  int      `json:"fixed_vs_zero"`
	Regressed              int      `json:"regressed_vs_zero"`
	CorrectWithCorrectLeft int      `json:"suffix_correct_given_correct_left"`
	Changes                []change `json:"changes"`
}
type change struct{ Left, LeftShown, Want, Before, After string }

func typeReading(s *session.Session, ys []zhuyin.Syllable) proto.Snapshot {
	var snap proto.Snapshot
	for _, y := range ys {
		for _, k := range layout.Standard.Keys(y) {
			snap = s.Handle(proto.Request{Type: proto.Key, Key: string(k)})
		}
	}
	return snap
}

// 只用固定參考詞界，選離句中最近且右方仍有內容的切點。
func splitPoint(words []string, n int) int {
	best, at := 0, 0
	for _, w := range words[:max(0, len(words)-1)] {
		at += utf8.RuneCountInString(w)
		if best == 0 || abs(2*at-n) < abs(2*best-n) {
			best = at
		}
	}
	return best
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func main() {
	data := flag.String("data", "build/release/word-boundaries-20260921/data", "受測資料包")
	reference := flag.String("reference-data", "build/data", "固定標音資料包")
	corpus := flag.String("sentences", "data/raw/cv/*.txt", "語料 glob")
	split := flag.String("split", "dev", "dev／test 沿用 eval.Split；all 用於外部已切分的保留語料")
	limit := flag.Int("limit", 1000, "最多可標音且有固定切點的案例")
	grid := flag.String("weights", "0,0.025,0.05,0.1,0.25,0.5,1", "混合權重；必須從零開始")
	flag.Parse()
	fail := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *limit < 1 || (*split != "dev" && *split != "test" && *split != "all") {
		fail(fmt.Errorf("invalid split or limit"))
	}
	var rows []*result
	for _, part := range strings.Split(*grid, ",") {
		w, err := strconv.ParseFloat(part, 64)
		fail(err)
		if !(w >= 0 && w <= 1) {
			fail(fmt.Errorf("invalid weight"))
		}
		rows = append(rows, &result{Weight: w})
	}
	if rows[0].Weight != 0 {
		fail(fmt.Errorf("first weight must be zero"))
	}
	engine, err := datadir.Open(*data)
	fail(err)
	ref, err := datadir.Open(*reference)
	fail(err)
	rd := eval.NewReader(ref.Model.Lex.(*lexicon.Compact), ref.Model.LM)
	files, err := filepath.Glob(*corpus)
	fail(err)
	if len(files) == 0 {
		fail(fmt.Errorf("no corpus files"))
	}
	var chunks []string
	for _, name := range files {
		f, err := os.Open(name)
		fail(err)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			chunks = append(chunks, eval.Chunks(sc.Text(), 2)...)
		}
		fail(sc.Err())
		f.Close()
	}
	sort.Strings(chunks)
	chunks = slices.Compact(chunks)
	chunks = eval.Split(chunks, *split)
	rand.New(rand.NewSource(1)).Shuffle(len(chunks), func(i, j int) { chunks[i], chunks[j] = chunks[j], chunks[i] })
	for _, text := range chunks {
		if rows[0].Cases >= *limit {
			break
		}
		ys, words, ok := rd.Segment(text)
		if !ok {
			continue
		}
		at := splitPoint(words, len(ys))
		if at <= 0 || at >= len(ys) {
			continue
		}
		runes := []rune(text)
		left, want := string(runes[:at]), string(runes[at:])
		before := ""
		for i, row := range rows {
			m := personal.NewModel(engine.Model.Lex, engine.Model.LM)
			s := session.New(m)
			s.Opt.DocumentContextWeight = row.Weight
			s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
			leftShown := typeReading(s, ys[:at]).Text()
			commit := s.Handle(proto.Request{Type: proto.Commit})
			s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: commit.ContextID})
			shown := typeReading(s, ys[at:]).Text()
			if i == 0 {
				before = shown
			}
			row.Cases++
			row.Characters += len(runes) - at
			row.Edits += eval.EditDistance(want, shown)
			if commit.ContextID != 0 {
				row.Tokens++
			}
			if leftShown == left {
				row.LeftCorrect++
			}
			if shown == want {
				row.Correct++
				if leftShown == left {
					row.CorrectWithCorrectLeft++
				}
			}
			if shown == want && before != want {
				row.Fixed++
			}
			if shown != want && before == want {
				row.Regressed++
			}
			if shown != before {
				row.Changes = append(row.Changes, change{left, leftShown, want, before, shown})
			}
		}
	}
	if rows[0].Cases == 0 {
		fail(fmt.Errorf("no eligible cases"))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	fail(enc.Encode(map[string]any{"split": *split, "limit": *limit, "data": eval.Provenance(*data), "reference": eval.Provenance(*reference), "results": rows, "method": "Sorted unique Han chunks; eval.Split; seed 1 shuffle; nearest-midpoint reference word boundary; fresh model for each case/weight; same actual left commit; suffix only; simulated document confirmation. This does not measure application acknowledgment coverage, arbitrary document edits or cross-commit learning."}))
}
