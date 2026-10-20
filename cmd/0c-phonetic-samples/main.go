// 0c-phonetic-samples annotates one deterministic Han span per document for offline training.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/datadir"
	"github.com/0c-biggo/0c-ime/internal/eval"
)

type document struct {
	ID    string   `json:"id"`
	Texts []string `json:"texts"`
}
type sample struct {
	ID          string `json:"id"`
	Left        string `json:"left"`
	Want        string `json:"want"`
	LeftReading string `json:"left_reading"`
	Reading     string `json:"reading"`
}

// Prefer a word boundary nearest the midpoint, and keep both sides within the pilot contract.
func splitPoint(words []string, n int) int {
	best, at := 0, 0
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	for _, word := range words[:max(0, len(words)-1)] {
		at += utf8.RuneCountInString(word)
		if at < 4 || n-at < 4 || at > 32 || n-at > 24 {
			continue
		}
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
func run(input, output, reference string) error {
	ref, err := datadir.Open(reference)
	if err != nil {
		return err
	}
	reader := eval.NewReader(ref.Model.Lex.(*lexicon.Compact), ref.Model.LM)
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer in.Close()
	rows := []sample{}
	seen := map[string]bool{}
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 8<<20)
	for scan.Scan() {
		var d document
		if err := json.Unmarshal(scan.Bytes(), &d); err != nil {
			return err
		}
		if d.ID == "" || seen[d.ID] {
			return fmt.Errorf("missing/duplicate document ID")
		}
		seen[d.ID] = true
		for _, text := range d.Texts {
			rs := []rune(text)
			if len(rs) < 8 || len(rs) > 48 {
				return fmt.Errorf("invalid span length")
			}
			for _, r := range rs {
				if !zhuyin.IsHan(r) {
					return fmt.Errorf("non-Han span")
				}
			}
			ys, words, ok := reader.Segment(text)
			if !ok {
				continue
			}
			at := splitPoint(words, len(ys))
			if at == 0 {
				continue
			}
			rows = append(rows, sample{d.ID, string(rs[:at]), string(rs[at:]), readingString(ys[:at]), readingString(ys[at:])})
			break
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(rows); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
func main() {
	input := flag.String("input", "", "ordered document JSONL with ordered texts")
	output := flag.String("output", "", "sample JSON array; must not exist")
	reference := flag.String("reference-data", "build/data", "frozen automatic reading dictionary")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "input/output required")
		os.Exit(1)
	}
	if err := run(*input, *output, *reference); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
