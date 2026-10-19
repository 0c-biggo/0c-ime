package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPreservesUnknownAndProvenance(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.jsonl")
	out := filepath.Join(dir, "out.jsonl")
	line := `{"case_id":"x","group_id":"g","document_key":"d","split":"train","source_sha256":"` + strings.Repeat("a", 64) + `","text":"嗎媽","pinyin":["ma","ma1"],"origin":"source fixture"}`
	if err := os.WriteFile(in, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(in, out, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		Group   string `json:"group_id"`
		Reading []struct {
			Tone   int    `json:"tone"`
			Status string `json:"tone_status"`
		} `json:"observed_reading"`
	}
	if err = json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	if row.Group != "g" || row.Reading[0].Tone != 0 || row.Reading[0].Status != "unknown" || row.Reading[1].Tone != 1 {
		t.Fatalf("%s", data)
	}
	if err = run(in, out, false); err == nil {
		t.Fatal("overwrote output")
	}
	bad := filepath.Join(dir, "bad.jsonl")
	_ = os.WriteFile(in, []byte(line+"\n{}\n"), 0600)
	if err = run(in, bad, false); err == nil {
		t.Fatal("accepted malformed record")
	}
	if _, err = os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("published incomplete output")
	}
}
