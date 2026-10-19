// 0c-pinyin-import 將附文字／來源群組的觀測拼音轉成注音，保留 unknown tone。
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"git.biggo.com/0c/ime/internal/phoneticdata"
	"os"
	"unicode/utf8"
)

func main() {
	input := flag.String("input", "", "來源 JSONL")
	output := flag.String("output", "", "新輸出 JSONL")
	zero := flag.Bool("zero-is-neutral", false, "來源規格明訂數字0為輕聲時啟用")
	flag.Parse()
	if err := run(*input, *output, *zero); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(input, output string, zero bool) (retErr error) {
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		// Windows does not allow deleting this output while its handle is open.
		retErr = errors.Join(retErr, out.Close())
		if retErr != nil {
			retErr = errors.Join(retErr, os.Remove(output))
		}
	}()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		var r struct {
			CaseID   string   `json:"case_id"`
			GroupID  string   `json:"group_id"`
			Document string   `json:"document_key"`
			Split    string   `json:"split"`
			Source   string   `json:"source_sha256"`
			Text     string   `json:"text"`
			Pinyin   []string `json:"pinyin"`
			Origin   string   `json:"origin"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return err
		}
		if _, err := hex.DecodeString(r.Source); err != nil {
			return fmt.Errorf("invalid source digest")
		}
		validSplit := r.Split == "train" || r.Split == "dev" || r.Split == "calibration" || r.Split == "test" || r.Split == "diagnostic"
		if r.CaseID == "" || r.GroupID == "" || r.Document == "" || len(r.Source) != 64 || r.Origin == "" || !validSplit || len(r.Pinyin) == 0 || len(r.Pinyin) != utf8.RuneCountInString(r.Text) {
			return fmt.Errorf("missing provenance or aligned supervision")
		}
		reading := make([]phoneticdata.Reading, len(r.Pinyin))
		for i, raw := range r.Pinyin {
			reading[i], err = phoneticdata.Pinyin(raw, zero)
			if err != nil {
				return fmt.Errorf("reading %d: %w", i, err)
			}
		}
		record := map[string]any{"schema": "zuin-observed-reading-v1", "case_id": r.CaseID, "group_id": r.GroupID, "document_key": r.Document, "split": r.Split, "source_sha256": r.Source, "origin": r.Origin, "input_kind": "observed_pinyin", "targets": []string{r.Text}, "observed_reading": reading, "zero_is_neutral": zero, "adjudication": "unreviewed", "regional_mapping": "not_applied"}
		if err = encoder.Encode(record); err != nil {
			return err
		}
	}
	return scanner.Err()
}
