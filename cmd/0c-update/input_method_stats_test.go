package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputMethodStats(t *testing.T) {
	base := t.TempDir()
	if inputMethodStats(base) != nil {
		t.Fatal("no file: nothing attached")
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(base, inputMethodStatsFile), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"schema":1,"pid":42,"version":"0.2.10","started_at":"2026-11-06T09:00:00Z","written_at":"2026-11-06T09:10:00Z",
		"activations":3,"deactivations":2,"keys":21,"activations_since_wake":1,"keys_since_wake":0,
		"last_activate_at":"2026-11-06T09:05:00Z","last_wake_at":"2026-11-06T09:04:00Z",
		"floating":{"rescues":1,"fields":1,"apps":{"com.google.Chrome":2}},"typed":"不該出現"}`)
	got := inputMethodStats(base)
	if got == nil || got["keys"] != 21 || got["keys_since_wake"] != 0 || got["last_wake_at"] != "2026-11-06T09:04:00Z" || got["version"] != "0.2.10" {
		t.Fatalf("stats: %#v", got)
	}
	if _, ok := got["last_key_at"]; ok {
		t.Fatal("missing times stay missing")
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "不該出現") || !strings.Contains(string(b), `"apps":{"com.google.Chrome":2}`) {
		t.Fatalf("only known fields are copied: %s", b)
	}
	write(`{"schema":2,"keys":1}`)
	if inputMethodStats(base) != nil {
		t.Fatal("unknown schema is not attached")
	}
	write(`{"schema":1,`)
	if inputMethodStats(base) != nil {
		t.Fatal("broken file is not attached")
	}
	write(`{"schema":1,"pad":"` + strings.Repeat("x", inputMethodStatsLimit) + `"}`)
	if inputMethodStats(base) != nil {
		t.Fatal("oversized file is not attached")
	}
}
