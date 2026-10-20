// 0c-context-recall-eval 凍結實際提交前文與後段候選，供離線神經重排消融。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

// 離線複製現行 documentScorer 契約，不改線上核心；每例以真 session 首選核對。
type contextScore struct {
	*personal.Model
	left   string
	weight float64
}

func (s *contextScore) mix(l, w string, f func(string, string) float64) float64 {
	if l != lm.BOS {
		return f(l, w)
	}
	return (1-s.weight)*f(lm.BOS, w) + s.weight*f(s.left, w)
}
func (s *contextScore) Prob(l, w string) float64           { return s.mix(l, w, s.Model.Prob) }
func (s *contextScore) SystemProb(l, w string) float64     { return s.mix(l, w, s.Model.SystemProb) }
func (s *contextScore) Evidence(l, w string) float64       { return s.mix(l, w, s.Model.Evidence) }
func (s *contextScore) PriorMass(l, w string) float64      { return s.mix(l, w, s.Model.PriorMass) }
func (s *contextScore) HasContext(l string) bool           { return l == lm.BOS || s.Model.HasContext(l) }
func (s *contextScore) DocumentContext() (string, float64) { return s.left, s.weight }

type sample struct {
	ID          string `json:"id"`
	Left        string `json:"left"`
	Want        string `json:"want"`
	LeftReading string `json:"left_reading"`
	Reading     string `json:"reading"`
}
type result struct {
	ID            string              `json:"id"`
	LeftReference string              `json:"left_reference"`
	LeftShown     string              `json:"left_shown"`
	LeftCorrect   bool                `json:"left_correct"`
	Confirmed     bool                `json:"confirmed"`
	Tail          string              `json:"tail"`
	Want          string              `json:"want"`
	Reading       []zhuyin.Syllable   `json:"reading"`
	Production    string              `json:"production"`
	Candidates    []decode.Hypothesis `json:"candidates"`
}

func typeReading(s *session.Session, ys []zhuyin.Syllable) proto.Snapshot {
	var snap proto.Snapshot
	for _, y := range ys {
		for _, k := range layout.Standard.Keys(y) {
			snap = s.Handle(proto.Request{Type: proto.Key, Key: string(k)})
		}
	}
	return snap
}
func splitPoint(words []string, n int) int {
	best, at := 0, 0
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	for _, w := range words[:max(0, len(words)-1)] {
		at += utf8.RuneCountInString(w)
		if best == 0 || abs(2*at-n) < abs(2*best-n) {
			best = at
		}
	}
	return best
}
func readingString(ys []zhuyin.Syllable) string {
	parts := make([]string, len(ys))
	for i, y := range ys {
		parts[i] = string(y)
	}
	return strings.Join(parts, " ")
}
func evaluate(lex lexicon.Source, sys lm.System, weight float64, c sample) (result, error) {
	ys, lys := zhuyin.ParseReading(c.Reading), zhuyin.ParseReading(c.LeftReading)
	if len(ys) == 0 || len(lys) == 0 || len(ys) != utf8.RuneCountInString(c.Want) || len(lys) != utf8.RuneCountInString(c.Left) {
		return result{}, fmt.Errorf("invalid input %s", c.ID)
	}
	m := personal.NewModel(lex, sys)
	s := session.New(m)
	s.Opt.DocumentContextWeight = weight
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	left := typeReading(s, lys)
	commit := s.Handle(proto.Request{Type: proto.Commit})
	if commit.Commit != left.Text() {
		return result{}, fmt.Errorf("commit differs from display %s", c.ID)
	}
	tail := ""
	if commit.ContextID != 0 {
		if len(left.Preedit) == 0 {
			return result{}, fmt.Errorf("context without left path")
		}
		tail = left.Preedit[len(left.Preedit)-1].Text
	}
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: commit.ContextID})
	suffix := typeReading(s, ys)
	var scorer decode.Scorer = m
	if tail != "" && weight > 0 {
		scorer = &contextScore{m, tail, weight}
	}
	opt := s.Opt
	d := decode.Decoder{Lex: m.Source(), Score: scorer, Beam: opt.Beam, Continue: opt.Continue, SeparateContinuation: opt.SeparateContinuation}
	candidates, _ := d.NBest(input.Exact{}.Expand(ys), nil, opt.PrefixAware, 8)
	if len(candidates) == 0 || candidates[0].Text != suffix.Text() {
		return result{}, fmt.Errorf("NBest/session identity mismatch %s: %q", c.ID, suffix.Text())
	}
	return result{c.ID, c.Left, left.Text(), left.Text() == c.Left, commit.ContextID != 0, tail, c.Want, ys, suffix.Text(), candidates}, nil
}
func main() {
	data := flag.String("data", "", "配對資料包")
	reference := flag.String("reference-data", "build/data", "固定參考標音資料包")
	corpus := flag.String("sentences", "data/raw/cv/*.txt", "語料glob")
	split := flag.String("split", "dev", "dev/test/all；all用於外部已凍結資料")
	limit := flag.Int("limit", 1000, "可切段案例數")
	offset := flag.Int("offset", 0, "略過排序後前幾個可切段案例；保留原ID")
	cases := flag.String("cases", "", "固定跨提交JSON；設定後不讀語料")
	output := flag.String("output", "", "候選JSONL，不覆蓋")
	flag.Parse()
	fail := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *data == "" || *output == "" || *limit < 1 || *offset < 0 || *offset > int(^uint(0)>>1)-*limit || !slices.Contains([]string{"dev", "test", "all"}, *split) {
		fail(fmt.Errorf("invalid flags"))
	}
	for _, name := range []string{*output, *output + ".meta.json"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			fail(fmt.Errorf("output exists/inaccessible"))
		}
	}
	eng, err := datadir.Open(*data)
	fail(err)
	ref, err := datadir.Open(*reference)
	fail(err)
	if eng.DocumentContextWeight == nil {
		fail(fmt.Errorf("paired runtime weight required"))
	}
	rd := eval.NewReader(ref.Model.Lex.(*lexicon.Compact), ref.Model.LM)
	sources := map[string]string{}
	var samples []sample
	skipped := 0
	if *cases != "" {
		b, err := os.ReadFile(*cases)
		fail(err)
		sources[*cases] = fmt.Sprintf("%x", sha256.Sum256(b))
		fail(json.Unmarshal(b, &samples))
	} else {
		names, err := filepath.Glob(*corpus)
		fail(err)
		if len(names) == 0 {
			fail(fmt.Errorf("no corpus"))
		}
		var texts []string
		for _, name := range names {
			b, err := os.ReadFile(name)
			fail(err)
			sources[name] = fmt.Sprintf("%x", sha256.Sum256(b))
			sc := bufio.NewScanner(strings.NewReader(string(b)))
			for sc.Scan() {
				texts = append(texts, eval.Chunks(sc.Text(), 2)...)
			}
			fail(sc.Err())
		}
		sort.Strings(texts)
		texts = eval.Split(slices.Compact(texts), *split)
		rand.New(rand.NewSource(1)).Shuffle(len(texts), func(i, j int) { texts[i], texts[j] = texts[j], texts[i] })
		for _, text := range texts {
			if len(samples) >= *offset+*limit {
				break
			}
			ys, words, ok := rd.Segment(text)
			if !ok {
				skipped++
				continue
			}
			at := splitPoint(words, len(ys))
			if at <= 0 || at >= len(ys) {
				skipped++
				continue
			}
			rs := []rune(text)
			samples = append(samples, sample{fmt.Sprintf("%s-%06d", *split, len(samples)), string(rs[:at]), string(rs[at:]), readingString(ys[:at]), readingString(ys[at:])})
		}
	}
	samples = samples[min(*offset, len(samples)):min(*offset+*limit, len(samples))]
	if len(samples) == 0 {
		fail(fmt.Errorf("no cases"))
	}
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	fail(err)
	enc := json.NewEncoder(f)
	start := time.Now()
	confirmed, leftCorrect := 0, 0
	for _, c := range samples {
		row, err := evaluate(eng.Model.Lex, eng.Model.LM, *eng.DocumentContextWeight, c)
		fail(err)
		fail(enc.Encode(row))
		if row.Confirmed {
			confirmed++
		}
		if row.LeftCorrect {
			leftCorrect++
		}
	}
	fail(f.Close())
	b, err := os.ReadFile(*output)
	fail(err)
	exe, err := os.Executable()
	fail(err)
	eb, err := os.ReadFile(exe)
	fail(err)
	meta := map[string]any{"cases": len(samples), "split": *split, "limit": *limit, "offset": *offset, "skipped_before_limit": skipped, "confirmed": confirmed, "left_correct": leftCorrect, "session_identity_passed": len(samples), "document_weight": *eng.DocumentContextWeight, "data": eval.Provenance(*data), "reference": eval.Provenance(*reference), "sources": sources, "output_sha256": fmt.Sprintf("%x", sha256.Sum256(b)), "executable_sha256": fmt.Sprintf("%x", sha256.Sum256(eb)), "seconds": time.Since(start).Seconds(), "method": "Actual session commit and confirmation, same passive learning; frozen NBest8 with production .075 document mixture. Full commit retained offline only; no private store loaded."}
	mb, err := json.MarshalIndent(meta, "", "  ")
	fail(err)
	fail(os.WriteFile(*output+".meta.json", append(mb, '\n'), 0600))
	fmt.Printf("cases=%d confirmed=%d left_correct=%d identity=%d\n", len(samples), confirmed, leftCorrect, len(samples))
}
