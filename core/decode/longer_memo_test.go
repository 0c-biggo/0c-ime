package decode

import (
	"math"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// prepareMass 記下的前綴保護（longerRemembered）跟每次重走（FreshMass）逐位置相同：按鍵圖一顆一顆加鍵、改中間的鍵、
// 退格、加上尾端的假設、一般的音節格，中間穿插學習（個人片語、版本改變）。
func TestLongerEndMemo(t *testing.T) {
	w := memoWorld()
	d := &Decoder{Lex: w.model.Source(), Score: w.model}
	ref := &Decoder{Lex: w.model.Source(), Score: w.model, FreshMass: true}
	rng := rand.New(rand.NewSource(5))
	hits := 0
	for round := range 30 {
		var reading []zhuyin.Syllable
		for len(reading) < 4+rng.Intn(6) {
			reading = append(reading, w.words[rng.Intn(len(w.words))].Reading...)
		}
		all := testGraphKeys(reading)
		var keys []string
		for step := 0; step < 2*len(all); step++ {
			switch op := rng.Intn(10); {
			case op < 6 && len(keys) < len(all):
				keys = append(keys, all[len(keys)])
			case op == 6 && len(keys) > 0:
				keys = keys[:len(keys)-1]
			case op == 7 && len(keys) > 2:
				keys = slices.Clone(keys)
				keys[rng.Intn(len(keys)-1)] = testGraphGroups[rng.Intn(len(testGraphGroups))]
			case op == 8:
				e := w.words[rng.Intn(len(w.words))]
				w.model.Learn(personal.Commit{Syls: e.Reading, Path: []personal.Seg{{Word: e.Surface, End: len(e.Reading), Manual: true}}})
			}
			var lat input.Lattice
			switch round % 3 {
			case 0:
				lat = testKeyGraph(keys)
			case 1:
				lat = testKeyGraph(keys)
				for p, row := range lat {
					for _, h := range row {
						if p+h.Width() == len(lat) {
							h.Abbreviated = true
							lat[p] = append(lat[p], h)
						}
					}
				}
			default:
				lat = input.Exact{}.Expand(reading[:min(len(reading), len(keys))])
			}
			if len(lat) == 0 {
				continue
			}
			before := len(d.longerMemo)
			d.prepareMass(lat)
			ref.prepareMass(lat)
			if !slices.Equal(d.longerEnd, ref.longerEnd) {
				t.Fatalf("round %d step %d: memo %v, fresh %v", round, step, d.longerEnd, ref.longerEnd)
			}
			if len(d.longerMemo) == before {
				hits++
			}
		}
	}
	if hits < 20 {
		t.Fatalf("the memo was never used (%d)", hits)
	}
}

type memoWorldT struct {
	model *personal.Model
	words []lexicon.Entry
}

// memoWorld 是有學到計數的小詞庫（前綴保護只在學習之後算）：80 個音節的單字、隨機的多字詞與 bigram。
func memoWorld() memoWorldT {
	rng := rand.New(rand.NewSource(51))
	bases := zhuyin.Inventory()
	slices.Sort(bases)
	var syls []zhuyin.Syllable
	for _, b := range bases {
		if len(syls) < 80 && rng.Intn(5) == 0 {
			syls = append(syls, b.WithTone(1+rng.Intn(5)))
		}
	}
	lex, sys := lexicon.NewMem(), lm.NewMem()
	var w memoWorldT
	char := 0x4E00
	add := func(surface string, reading []zhuyin.Syllable) {
		e := lexicon.Entry{Surface: surface, Reading: reading, PRW: 1, Correction: rng.Intn(40) == 0}
		lex.Add(e)
		sys.SetUni(surface, math.Exp(-4-8*rng.Float64()))
		w.words = append(w.words, e)
	}
	for _, y := range syls {
		for range 1 + rng.Intn(3) {
			add(string(rune(char)), []zhuyin.Syllable{y})
			char++
		}
	}
	singles := slices.Clone(w.words)
	for range 1200 {
		var surface strings.Builder
		var reading []zhuyin.Syllable
		for range 2 + rng.Intn(3) {
			s := singles[rng.Intn(len(singles))]
			surface.WriteString(s.Surface)
			reading = append(reading, s.Reading...)
		}
		add(surface.String(), reading)
	}
	w.model = personal.NewModel(lex, sys)
	for range 20 {
		e := w.words[rng.Intn(len(w.words))]
		w.model.Learn(personal.Commit{Syls: e.Reading, Path: []personal.Seg{{Word: e.Surface, End: len(e.Reading), Manual: true}}})
	}
	return w
}

var testGraphGroups = []string{"ㄅㄉㄚ", "ㄍㄐㄞㄧ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄟㄨ", "ㄔㄘㄣ", "ㄇㄋㄜㄝ", "ㄏㄒㄠㄩ", "ㄕㄙㄤㄥ", "ㄈㄌㄡㄖ"}

func testGraphKeys(reading []zhuyin.Syllable) []string {
	var out []string
	for _, y := range reading {
		for _, r := range string(y.Base()) {
			for _, g := range testGraphGroups {
				if slices.Contains([]rune(g), r) {
					out = append(out, g)
					break
				}
			}
		}
	}
	return out
}

// testKeyGraph 是 keys 的按鍵圖：每 1–3 顆鍵組得出的合法音節（五個聲調）。
func testKeyGraph(keys []string) input.Lattice {
	lat := make(input.Lattice, len(keys))
	for p := range keys {
		for w := 1; w <= 3 && p+w <= len(keys); w++ {
			var spell func(i int, acc string)
			spell = func(i int, acc string) {
				if i == p+w {
					if y := zhuyin.Syllable(acc); y.Legal() {
						for _, v := range zhuyin.Untone(y).Tones() {
							lat[p] = append(lat[p], input.Hyp{Syl: v, Untoned: true, Span: w})
						}
					}
					return
				}
				for _, r := range keys[i] {
					spell(i+1, acc+string(r))
				}
			}
			spell(p, "")
		}
	}
	return lat
}
