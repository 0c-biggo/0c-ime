package english

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCommonNeighbourIsNotHiddenByCandidateLimit(t *testing.T) {
	var words []Word
	for at := 0; at < 6; at++ {
		for c := 'a'; c <= 'z'; c++ {
			if c == 'x' {
				continue
			}
			text := []byte("xxxxxx")
			text[at] = byte(c)
			words = append(words, Word{Text: string(text), Weight: 40})
		}
	}
	words = append(words, Word{Text: "xxeexx", Weight: 100})
	d := New(words)
	for _, c := range d.Corrections("xxxxxx", MaxCandidates) {
		if c.Text == "xxeexx" {
			t.Fatal("fixture should hide the distance-two common word")
		}
	}
	if !d.NearCommon("xxxxxx", 50) || d.NearCommon("xxxxxx", 101) || d.NearCommon("xxeexx", 50) {
		t.Fatal("common-neighbour protection was truncated or changed valid spelling")
	}
}

func TestIndexCoversEntireDictionaryAndPrefixRange(t *testing.T) {
	var words []Word
	for i := 0; i < 6000; i++ {
		// All entries precede the correction target; over 128 share a prefix.
		n := i
		suffix := ""
		for range 4 {
			suffix += string(rune('a' + n%26))
			n /= 26
		}
		words = append(words, Word{"a" + suffix, 1})
	}
	words = append(words, Word{"azzzz", 100}, Word{"zebra", 90})
	d := New(words)
	if got := d.Prefix("a", 1); len(got) != 1 || got[0].Text != "azzzz" {
		t.Fatalf("prefix truncated: %+v", got)
	}
	if got := d.Corrections("zebar", 1); len(got) != 1 || got[0].Text != "zebra" {
		t.Fatalf("correction truncated: %+v", got)
	}
}

func TestIndexedCorrectionMatchesExhaustiveDistance(t *testing.T) {
	var words []Word
	// Exhaustively exercise overlapping insertions, deletion and transposition.
	var generate func(string, int)
	generate = func(p string, n int) {
		if p != "" {
			words = append(words, Word{p, float64(len(words) + 1)})
		}
		if n > 0 {
			for _, c := range "abc" {
				generate(p+string(c), n-1)
			}
		}
	}
	generate("", 5)
	d := New(words)
	for _, w := range words {
		for _, typed := range []string{w.Text + "x", "x" + w.Text, w.Text + "xx", strings.ReplaceAll(w.Text, "a", "x")} {
			if len(typed) < 3 || d.Has(typed) {
				continue
			}
			maxEdits := 1
			if len(typed) >= 6 {
				maxEdits = 2
			}
			var want []Candidate
			for _, candidate := range d.words {
				if edit := distance(typed, candidate.Text); edit <= maxEdits {
					want = append(want, Candidate{candidate.Text, edit, candidate.Weight})
				}
			}
			rank(want)
			want = want[:min(len(want), 5)]
			if got := d.Corrections(typed, 5); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: got %+v want %+v", typed, got, want)
			}
		}
	}
}

func TestNoSuggestWordsRecognizedAndUnicodeIdentifiersUntouched(t *testing.T) {
	d := NewFiltered([]Word{{"example", 9}, {"examples", 3}, {"excluded", 100}}, []string{"excluded"})
	if !d.Has("excluded") || len(d.Prefix("excl", 3)) != 0 || len(d.Corrections("excludde", 3)) != 0 {
		t.Fatal("no-suggest behavior")
	}
	for _, text := range []string{"K", "ſearch", "api_v2", "foo123", strings.Repeat("x", 1000)} {
		if d.Has(text) || len(d.Prefix(text, 5)) > 0 || len(d.Corrections(text, 5)) > 0 {
			t.Fatal(fmt.Sprintf("non-word queried: %q", text))
		}
	}
}

func TestCodecRoundTripAndMalformedInput(t *testing.T) {
	d := NewFiltered([]Word{{"example", 5}, {"excluded", 10}}, []string{"excluded"})
	b := d.Encode()
	back, err := Decode(b)
	if err != nil || !reflect.DeepEqual(b, back.Encode()) || !back.Has("excluded") || len(back.Prefix("excl", 2)) != 0 {
		t.Fatalf("roundtrip: %v", err)
	}
	for i := 0; i < len(b); i++ {
		if _, err := Decode(b[:i]); err == nil {
			t.Fatalf("accepted truncated input %d", i)
		}
	}
	if _, err := Decode(append(b, 0)); err == nil {
		t.Fatal("trailing data accepted")
	}
	bad := append([]byte(nil), b...)
	bad[len(magic)] = 255
	bad[len(magic)+1] = 255
	bad[len(magic)+2] = 255
	if _, err := Decode(bad); err == nil {
		t.Fatal("unbounded word count accepted")
	}
}
