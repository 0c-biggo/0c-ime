package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicPackPreflight(t *testing.T) {
	for _, scenario := range []string{"valid", "restricted", "missing-license", "unknown-license", "missing-version"} {
		t.Run(scenario, func(t *testing.T) {
			r := Build(load(t, fixture), DefaultOptions())
			if len(r.Errors) != 0 {
				t.Fatal(r.Errors)
			}
			licenses := t.TempDir()
			for i := range r.Sources {
				s := &r.Sources[i]
				s.Distributable, s.License = true, "CC0-1.0"
				s.URL, s.Version, s.Attribution = "https://example.org/source", "fixture-v1", "Fixture authors"
				if err := os.WriteFile(filepath.Join(licenses, s.ID+".txt"), []byte("Fixture statement"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "restricted":
				r.Sources[0].Distributable = false
			case "missing-license":
				os.Remove(filepath.Join(licenses, r.Sources[0].ID+".txt"))
			case "unknown-license":
				r.Sources[0].License = "未定"
			case "missing-version":
				r.Sources[0].Version = "-"
			}
			o := DefaultOptions()
			o.Public = true
			out := filepath.Join(t.TempDir(), "pack")
			err := r.Write(out, licenses, o)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(out, "NOTICE.txt"))
				if err != nil || !strings.Contains(string(b), "Fixture statement") {
					t.Fatalf("NOTICE: %s, %v", b, err)
				}
			} else {
				if err == nil {
					t.Fatal("public pack must reject incomplete sources")
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("rejection created output")
				}
				// 研究用途仍可輸出相同來源，不偷改既有研究條件。
				o.Public = false
				if err := r.Write(out, licenses, o); err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
				o.Public = true
				if err := r.Write(out, licenses, o); err == nil {
					t.Fatal("expected rejection")
				}
				after, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
				if string(before) != string(after) {
					t.Fatal("rejection modified existing pack")
				}
			}
		})
	}
}
