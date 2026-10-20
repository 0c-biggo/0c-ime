package englishpack

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
)

func fixtureSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	tsv := []byte("example\t35\t0\nexcluded\t50\t1\n")
	license := []byte("Example copyright and permission text\n")
	meta := SourceMetadata{Source: Source{Name: "Test source", Version: "1", URL: "https://example.test", SHA256: Sum([]byte("upstream")), License: "test", Attribution: "test author"}, NormalizedSHA256: Sum(tsv), LicenseSHA256: Sum(license), Words: 2}
	mb, _ := json.Marshal(meta)
	for name, b := range map[string][]byte{"esdb.tsv": tsv, "LICENSE.txt": license, "source.json": mb, "technical.tsv": []byte("example\t300\n")} {
		if err := os.WriteFile(filepath.Join(src, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func TestCompileDeterministicAndNoSuggestRoundTrip(t *testing.T) {
	src := fixtureSource(t)
	out1, out2 := filepath.Join(t.TempDir(), "pack"), filepath.Join(t.TempDir(), "pack")
	for _, out := range []string{out1, out2} {
		if err := Compile(src, out); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"words.bin", "manifest.json", "NOTICE.txt"} {
		a, _ := os.ReadFile(filepath.Join(out1, name))
		b, _ := os.ReadFile(filepath.Join(out2, name))
		if !bytes.Equal(a, b) {
			t.Fatalf("nondeterministic %s", name)
		}
	}
	d, m, err := Load(out1)
	if err != nil || d.Len() != 2 || m.Words != 2 || !d.Has("excluded") || len(d.Prefix("excl", 5)) != 0 || d.Prefix("ex", 1)[0].Weight != 300 {
		t.Fatalf("load: %v", err)
	}
	if err := Compile(src, out1); err == nil {
		t.Fatal("overwrote existing pack")
	}
}

func TestRejectedSourceDoesNotPublishOrOverwrite(t *testing.T) {
	for _, name := range []string{"esdb.tsv", "LICENSE.txt"} {
		src := fixtureSource(t)
		out := filepath.Join(t.TempDir(), "pack")
		os.WriteFile(filepath.Join(src, name), []byte("tampered"), 0600)
		if err := Compile(src, out); err == nil {
			t.Fatal("unverified source accepted")
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("rejected source published output")
		}
	}
	src := fixtureSource(t)
	os.WriteFile(filepath.Join(src, "technical.tsv"), []byte("example\tNaN\n"), 0600)
	if err := Compile(src, filepath.Join(t.TempDir(), "pack")); err == nil {
		t.Fatal("invalid ranking prior accepted")
	}
}

func TestLoadRejectsCorruptionAndMissingAttribution(t *testing.T) {
	for _, name := range []string{"words.bin", "NOTICE.txt", "manifest.json"} {
		out := filepath.Join(t.TempDir(), "pack")
		if err := Compile(fixtureSource(t), out); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(out, name), []byte("tampered"), 0600)
		if _, _, err := Load(out); err == nil {
			t.Fatalf("accepted corrupted %s", name)
		}
	}
	if err := Write(filepath.Join(t.TempDir(), "pack"), english.Prototype(), Manifest{Ranking: "test"}, []byte("test")); err == nil {
		t.Fatal("missing source metadata accepted")
	}
}

func TestImportListsMatchesReviewedNormalization(t *testing.T) {
	src := fixtureSource(t)
	dir := t.TempDir()
	mb, _ := os.ReadFile(filepath.Join(src, "source.json"))
	var m SourceMetadata
	json.Unmarshal(mb, &m)
	for _, size := range []int{10, 20, 35, 40, 50, 55, 60} {
		data := []byte("Example\nexample\nexcluded!\nfoo_bar\nword's\n")
		name := filepath.Base(filepath.Join(dir, fmtSize(size)))
		os.WriteFile(filepath.Join(dir, name), data, 0600)
		m.Exports = append(m.Exports, Export{size, name, Sum(data)})
	}
	want := []byte("example\t10\t0\nexcluded\t10\t1\n")
	m.NormalizedSHA256 = Sum(want)
	mb, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(src, "source.json"), mb, 0600)
	if err := ImportLists(src, dir); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(src, "esdb.tsv"))
	if !bytes.Equal(got, want) {
		t.Fatal("normalization differs")
	}
	os.WriteFile(filepath.Join(dir, "60.txt"), []byte("tampered"), 0600)
	if err := ImportLists(src, dir); err == nil {
		t.Fatal("unverified export accepted")
	}
	got, _ = os.ReadFile(filepath.Join(src, "esdb.tsv"))
	if !bytes.Equal(got, want) {
		t.Fatal("failed import changed source")
	}
}

func fmtSize(n int) string { return strconv.Itoa(n) + ".txt" }
