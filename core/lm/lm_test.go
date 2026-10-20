package lm

import (
	"math"
	"os"
	"testing"
)

func TestCompactMatchesMem(t *testing.T) {
	f, err := os.Open("../../testdata/toy/lm.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuildCompact(m.Entries())
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeCompact(built.Encode())
	if err != nil {
		t.Fatal(err)
	}
	uni, bi := m.Entries()
	words := []string{"不存在", BOS, ""}
	for w, p := range uni {
		words = append(words, w)
		if got := c.Uni(w); float32(got) != float32(p) {
			t.Errorf("Uni(%q) = %g 應為 %g", w, got, p)
		}
	}
	for _, l := range words {
		for _, w := range words {
			// bigram 機率量化為 1 位元組：相對誤差須在半階（約 ±3.7%）內
			got, want := c.Bi(l, w), bi[[2]string{l, w}]
			if (want == 0) != (got == 0) || want > 0 && math.Abs(got/want-1) > 0.037 {
				t.Errorf("Bi(%q,%q) = %g 應約為 %g", l, w, got, want)
			}
		}
	}
	for _, l := range words {
		if c.HasLeft(l) != m.HasLeft(l) {
			t.Errorf("HasLeft(%q)", l)
		}
	}
	n := 0
	c.EachBigram(func(l, w string, p float64) {
		n++
		if p != c.Bi(l, w) {
			t.Errorf("EachBigram(%q,%q) = %g，Bi 為 %g", l, w, p, c.Bi(l, w))
		}
	})
	if n != len(bi) || n != c.Bigrams() {
		t.Errorf("EachBigram 列出 %d 筆，應為 %d", n, len(bi))
	}
	if c.Uni("不存在") != Unseen {
		t.Error("未登錄詞")
	}
	if _, err := BuildCompact(map[string]float64{"a": 0.1}, map[[2]string]float64{{"a", "b"}: 0.1}); err == nil {
		t.Error("bigram 的詞必須有 unigram")
	}
	b := built.Encode()
	b[len(b)/2] ^= 1
	if _, err := DecodeCompact(b); err == nil {
		t.Error("CRC 應抓到損毀")
	}
}

func TestQuantize(t *testing.T) {
	for _, p := range []float64{1, 0.5, 0.1, 1e-3, 1e-6, 1e-8} {
		if got := dequantize(quantize(p)); math.Abs(got/p-1) > 0.037 {
			t.Errorf("%g → %g", p, got)
		}
	}
	if quantize(1e-12) != 255 || quantize(1) != 0 {
		t.Error("超出範圍應夾在兩端")
	}
}
