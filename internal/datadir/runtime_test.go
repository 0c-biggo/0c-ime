package datadir

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestPackRuntimeCalibration(t *testing.T) {
	dir := t.TempDir()
	lex, err := lexicon.Build([]lexicon.Entry{{Surface: "甲", Reading: zhuyin.ParseReading("ㄐㄧㄚˇ"), PRW: 1}})
	if err != nil {
		t.Fatal(err)
	}
	sys, err := lm.BuildCompact(map[string]float64{"甲": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]any{}
	for name, data := range map[string][]byte{"lexicon.bin": lex.Encode(), "lm.bin": sys.Encode()} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		files[name] = map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	for _, tc := range []struct {
		name, config string
		want         float64
		bad          bool
	}{
		{"legacy", "", .1, false}, {"null", "null", .1, false},
		{"calibrated", `{"version":1,"document_context_weight":0.075}`, .075, false},
		{"disabled", `{"version":1,"document_context_weight":0}`, 0, false},
		{"missing", `{"version":1}`, 0, true},
		{"negative", `{"version":1,"document_context_weight":-0.1}`, 0, true},
		{"large", `{"version":1,"document_context_weight":1.1}`, 0, true},
		{"string", `{"version":1,"document_context_weight":"0.1"}`, 0, true},
		{"null-weight", `{"version":1,"document_context_weight":null}`, 0, true},
		{"future-version", `{"version":2,"document_context_weight":0.1}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{"format": 1, "files": files}
			if tc.config != "" {
				m["runtime"] = json.RawMessage(tc.config)
				if tc.config != "null" {
					m["format"] = 2
				}
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			eng, err := Open(dir)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid calibration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			first := eng.OpenSession()
			if got := eng.Session(first).Opt.DocumentContextWeight; got != tc.want {
				t.Fatalf("first session weight %g, want %g", got, tc.want)
			}
			eng.Session(first).Opt.DocumentContextWeight = .9
			if got := eng.Session(eng.OpenSession()).Opt.DocumentContextWeight; got != tc.want {
				t.Fatalf("session override leaked: %g", got)
			}
		})
	}
	// 舊 reader 必須拒絕格式 2，而非忽略 runtime 後使用錯誤權重。
	for _, text := range []string{
		`{"format":1,"runtime":{"version":1,"document_context_weight":0.05}}`,
		`{"format":2}`, `{"format":3}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatal("ambiguous runtime/format combination accepted")
		}
	}
}
