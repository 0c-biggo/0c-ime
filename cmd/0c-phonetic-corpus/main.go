// 0c-phonetic-corpus 串流標注純漢字訓練片段；沒有已確認前文，不從答案補前文。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
	"git.biggo.com/0c/ime/internal/datadir"
	"git.biggo.com/0c/ime/internal/eval"
	"os"
)

type inputRow struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type outputRow struct {
	ID        string            `json:"id"`
	Reading   []zhuyin.Syllable `json:"reading"`
	Words     []string          `json:"words"`
	Supported bool              `json:"supported"`
}

func validText(text string) bool {
	rs := []rune(text)
	if len(rs) < 2 || len(rs) > 32 {
		return false
	}
	for _, r := range rs {
		if !zhuyin.IsHan(r) {
			return false
		}
	}
	return true
}
func run(input, output, data string) error {
	eng, err := datadir.Open(data)
	if err != nil {
		return err
	}
	reader := eval.NewReader(eng.Model.Lex.(*lexicon.Compact), eng.Model.LM)
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	seen := map[string]bool{}
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		var row inputRow
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			out.Close()
			return err
		}
		if row.ID == "" || seen[row.ID] || !validText(row.Text) {
			out.Close()
			return fmt.Errorf("invalid/duplicate span")
		}
		seen[row.ID] = true
		reading, words, ok := reader.Segment(row.Text)
		if err := enc.Encode(outputRow{row.ID, reading, words, ok}); err != nil {
			out.Close()
			return err
		}
	}
	if err := scan.Err(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
func main() {
	input := flag.String("input", "", "輸入 JSONL")
	output := flag.String("output", "", "輸出 JSONL，不覆寫")
	data := flag.String("reference-data", "build/data", "固定參考標音包")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "input/output required")
		os.Exit(1)
	}
	if err := run(*input, *output, *data); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
