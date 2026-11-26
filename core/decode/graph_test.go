package decode_test

import (
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 按鍵圖（注音九宮格自動切字）：位置是九宮格的鍵，一個音節佔 1–3 顆鍵。

var graphGroups = []string{"ㄅㄉㄚ", "ㄍㄐㄞㄧ", "ㄓㄗㄢㄦ", "ㄆㄊㄛ", "ㄎㄑㄟㄨ", "ㄔㄘㄣ", "ㄇㄋㄜㄝ", "ㄏㄒㄠㄩ", "ㄕㄙㄤㄥ", "ㄈㄌㄡㄖ"}

// graphKeys 是讀音在三星式九宮格上的鍵（每個注音一顆鍵）。
func graphKeys(reading []zhuyin.Syllable) []string {
	var out []string
	for _, y := range reading {
		for _, r := range string(y.Base()) {
			for _, g := range graphGroups {
				if strings.ContainsRune(g, r) {
					out = append(out, g)
				}
			}
		}
	}
	return out
}

// spellings 是每顆鍵各取一個注音、照「聲母 介音 韻母」順序組得出的完整音節（不帶聲調）。
func spellings(keys []string) []zhuyin.Syllable {
	var out []zhuyin.Syllable
	var walk func(i int, last zhuyin.Class, b []rune)
	walk = func(i int, last zhuyin.Class, b []rune) {
		if i == len(keys) {
			if y := zhuyin.Syllable(string(b)); y.Legal() {
				out = append(out, y)
			}
			return
		}
		for _, r := range keys[i] {
			if cl := zhuyin.ClassOf(r); cl > last && cl != zhuyin.Tone {
				walk(i+1, cl, append(b[:len(b):len(b)], r))
			}
		}
	}
	walk(0, zhuyin.None, nil)
	return out
}

// keyGraph 是鍵 keys 的按鍵圖：每 1–3 顆連續的鍵組得出合法音節就有一條弧，展開成五個聲調。untoned 時標成
// 沒打聲調（實際的九宮格），否則不標（弧不裁切，方便跟逐一切法的音節格比對）。
func keyGraph(keys []string, untoned bool) input.Lattice {
	lat := make(input.Lattice, len(keys))
	for p := range keys {
		for w := 1; w <= 3 && p+w <= len(keys); w++ {
			for _, b := range spellings(keys[p : p+w]) {
				for _, y := range zhuyin.Untone(b).Tones() {
					lat[p] = append(lat[p], input.Hyp{Syl: y, Untoned: untoned, Span: w})
				}
			}
		}
	}
	return lat
}

// withTail 在走到終點的假設後面多加一份標成簡拼的（同 session 的尾端展開：只有走到最後一顆鍵的有）。
func withTail(lat input.Lattice) input.Lattice {
	out := make(input.Lattice, len(lat))
	for p, row := range lat {
		out[p] = slices.Clone(row)
		for _, h := range row {
			if p+h.Width() == len(lat) {
				h.Abbreviated = true
				out[p] = append(out[p], h)
			}
		}
	}
	return out
}

// segmentations 列出 n 顆鍵每段 1–3 顆的所有切法（每段的終點）。
func segmentations(n int) [][]int {
	if n == 0 {
		return [][]int{nil}
	}
	var out [][]int
	for w := 1; w <= 3 && w <= n; w++ {
		for _, rest := range segmentations(n - w) {
			cuts := []int{w}
			for _, c := range rest {
				cuts = append(cuts, w+c)
			}
			out = append(out, cuts)
		}
	}
	return out
}

// restrict 是按鍵圖照切法 cuts 走的一般音節格（每個位置一個音節）；某一段沒有讀音時回傳 nil。
func restrict(lat input.Lattice, cuts []int) input.Lattice {
	out := make(input.Lattice, len(cuts))
	p := 0
	for i, c := range cuts {
		for _, h := range lat[p] {
			if h.Width() == c-p {
				h.Span = 0
				out[i] = append(out[i], h)
			}
		}
		if len(out[i]) == 0 {
			return nil
		}
		p = c
	}
	return out
}

func joined(path []decode.Seg) string {
	var b strings.Builder
	for _, g := range path {
		b.WriteString(g.Word)
	}
	return b.String()
}

// plainScore 只露出機率與上下文：沒有學到的計數，也就沒有前綴保護。前綴保護看「從這個起點有沒有更長的完整詞」，
// 按鍵圖上那個詞可能走另一種切法，跟只有一種切法的音節格不同，這是設計（見 prepareMass），不是搜尋的差別。
type plainScore struct{ m *personal.Model }

func (p plainScore) Prob(left, w string) float64 { return p.m.Prob(left, w) }
func (p plainScore) HasContext(left string) bool { return p.m.HasContext(left) }

// 按鍵圖的最佳路徑就是所有切法各自最佳路徑裡最好的那個（不裁切、不限 beam、沒有前綴保護時），分數逐位元相同；
// 路徑接得起來，每段的音節數等於字數。
func TestKeyGraphBestIsBestSegmentation(t *testing.T) {
	w := goldenModel()
	rng := rand.New(rand.NewSource(3))
	checked := 0
	for range 150 {
		var reading []zhuyin.Syllable
		for len(reading) < 1+rng.Intn(4) {
			reading = append(reading, w.words[rng.Intn(len(w.words))].Reading...)
		}
		keys := graphKeys(reading)
		if len(keys) > 9 {
			continue
		}
		lat := keyGraph(keys, false)
		for _, prefix := range []bool{false, true} {
			d := &decode.Decoder{Lex: w.model.Source(), Score: plainScore{w.model}, Continue: 0.5, SeparateContinuation: true, TopB: 1 << 20}
			path, score := d.Best(lat, nil, prefix)
			bestScore, bestText := score-1, ""
			found := false
			for _, cuts := range segmentations(len(keys)) {
				sub := restrict(lat, cuts)
				if sub == nil {
					continue
				}
				p, s := d.Best(sub, nil, prefix)
				if len(p) > 0 && (!found || s > bestScore) {
					bestScore, bestText, found = s, joined(p), true
				}
			}
			if !found {
				if len(path) != 0 {
					t.Fatalf("%v: graph path %v but no segmentation decodes", keys, path)
				}
				continue
			}
			if score != bestScore || joined(path) != bestText {
				t.Fatalf("%v prefix=%v: graph %q %v, best segmentation %q %v", keys, prefix, joined(path), score, bestText, bestScore)
			}
			// 路徑接得起來，每段的音節數等於字數。
			at := 0
			for _, g := range path {
				if g.Start != at || len([]rune(g.Word)) != g.Syllables() {
					t.Fatalf("%v: path %+v", keys, path)
				}
				at = g.End
			}
			checked++
		}
	}
	if checked < 100 {
		t.Fatalf("only %d cases checked", checked)
	}
}

// 逐鍵增量（Search）跟每次從頭解（Best）逐位元相同：打字、在中間改鍵、退格、加減鎖定，含學過的個人資料
// （前綴保護）與尾端前綴。
func TestSearchMatchesBest(t *testing.T) {
	w := goldenModel()
	rng := rand.New(rand.NewSource(11))
	for round := range 40 {
		var reading []zhuyin.Syllable
		for len(reading) < 3+rng.Intn(5) {
			reading = append(reading, w.words[rng.Intn(len(w.words))].Reading...)
		}
		all := graphKeys(reading)
		var s decode.Search
		d := &decode.Decoder{Lex: w.model.Source(), Score: w.model, Beam: 8, Continue: 0.5, SeparateContinuation: true}
		ref := &decode.Decoder{Lex: w.model.Source(), Score: w.model, Beam: 8, Continue: 0.5, SeparateContinuation: true}
		var keys []string
		locks := map[int]decode.Lock{}
		reused := 0
		for step := 0; step < 3*len(all); step++ {
			switch op := rng.Intn(10); {
			case op < 7 && len(keys) < len(all):
				keys = append(keys, all[len(keys)])
			case op == 7 && len(keys) > 0:
				keys = keys[:len(keys)-1]
			case op == 8 && len(keys) > 2:
				i := rng.Intn(len(keys) - 1)
				keys = slices.Clone(keys)
				keys[i] = graphGroups[rng.Intn(len(graphGroups))]
			default:
				clear(locks)
				if path, _ := ref.Best(keyGraph(keys, true), nil, false); len(path) > 1 && rng.Intn(2) == 0 {
					g := path[0]
					locks[g.Start] = decode.Lock{Word: g.Word, Len: g.End - g.Start}
				}
			}
			for at, l := range locks {
				if at+l.Len > len(keys) {
					delete(locks, at)
				}
			}
			lat := keyGraph(keys, true)
			if round%4 >= 2 {
				lat = withTail(lat) // 尾端的假設每一鍵都會變（session 的還沒打完的音節）
			}
			if round%3 == 0 {
				lat = withCorrected(lat) // 觸控修正的假設另外裁切（#335）
			}
			prefix := round%2 == 0
			got, gotScore := d.BestSearch(&s, lat, locks, prefix)
			want, wantScore := ref.Best(lat, locks, prefix)
			if gotScore != wantScore || !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d step %d %v: search %v %v, best %v %v", round, step, keys, got, gotScore, want, wantScore)
			}
			if len(keys) > 3 {
				reused = max(reused, s.Reused)
			}

		}
		if reused < 3 {
			t.Fatalf("round %d: search never reused states (%d)", round, reused)
		}
	}
}

// 一個例子：ㄒㄧㄢ 三顆鍵可以是一個音節（先），也可以是兩個（西安）。詞庫、候選、Fits、讀音還原都看得到兩種切法，
// 鎖定只能落在音節邊界上。
func graphFixture() (*decode.Decoder, lexicon.Source) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"先", "ㄒㄧㄢ", 4e-3}, {"西", "ㄒㄧ", 3e-3}, {"安", "ㄢ", 2e-3}, {"西安", "ㄒㄧ ㄢ", 1e-3}, {"鮮", "ㄒㄧㄢ", 1e-3},
		{"去", "ㄑㄩˋ", 3e-3}, {"西安市", "ㄒㄧ ㄢ ㄕˋ", 5e-4}, {"市", "ㄕˋ", 2e-3}, {"先去", "ㄒㄧㄢ ㄑㄩˋ", 1e-3},
		{"嘻嘻", "ㄒㄧ ㄒㄧ", 1e-5},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	m := personal.NewModel(lex, sys)
	return &decode.Decoder{Lex: m.Source(), Score: m, Continue: 0.5, SeparateContinuation: true}, m.Source()
}

func TestKeyGraphCutsAndCandidates(t *testing.T) {
	d, lex := graphFixture()
	keys := graphKeys(zhuyin.ParseReading("ㄒㄧㄢ"))
	lat := keyGraph(keys, true)
	path, _ := d.Best(lat, nil, false)
	if joined(path) != "先" || path[0].Cuts != nil && !slices.Equal(path[0].Ends(), []int{3}) || path[0].Syllables() != 1 {
		t.Fatalf("path %+v", path)
	}
	var got []string
	for _, c := range d.Candidates(lat, 0, 3, "", nil) {
		got = append(got, c.Entry.Surface)
	}
	for _, w := range []string{"先", "鮮", "西安"} {
		if !slices.Contains(got, w) {
			t.Fatalf("candidates %v lack %s", got, w)
		}
	}
	if fits, _ := d.FitsIn(lat, 0, 3, "西安"); !fits {
		t.Fatal("西安 should fit three keys")
	}
	if fits, _ := d.FitsIn(lat, 0, 2, "西安"); fits {
		t.Fatal("西安 does not fit two keys")
	}
	r, cuts, ok := decode.ReadingCuts(lex, lat, 0, 3, "西安")
	if !ok || zhuyin.Key(r) != zhuyin.Key(zhuyin.ParseReading("ㄒㄧ ㄢ")) || !slices.Equal(cuts, []int{2, 3}) {
		t.Fatalf("reading %v cuts %v", r, cuts)
	}
	// 鎖定「安」（鍵 2–3）：整段只能是「西安」那種切法；鎖定在音節中間（鍵 1–3）的「安」沒有路徑。
	locked, _ := d.Best(lat, map[int]decode.Lock{2: {Word: "安", Len: 1}}, false)
	if joined(locked) != "西安" {
		t.Fatalf("locked path %+v", locked)
	}
	if p, _ := d.Best(lat, map[int]decode.Lock{1: {Word: "安", Len: 2}}, false); len(p) != 0 {
		t.Fatalf("lock inside a syllable decoded %+v", p)
	}
	// 候選視窗：ㄒㄧㄢ＋ㄕˋ 的前三顆鍵，右邊的「市」讓「西安」（西安市）排第一。
	keys = append(keys, graphKeys(zhuyin.ParseReading("ㄕˋ"))...)
	lat = keyGraph(keys, true)
	cands := d.CandidatesInContext(lat, 0, 3, nil, nil)
	if len(cands) == 0 || cands[0].Entry.Surface != "西安" {
		t.Fatalf("candidates in context %+v", cands)
	}
	// NBest 的第一名就是 Best。
	best, score := d.Best(lat, nil, true)
	rows, _ := d.NBest(lat, nil, true, 5)
	if len(rows) == 0 || rows[0].Text != joined(best) || rows[0].LogScore != score {
		t.Fatalf("nbest %+v, best %+v", rows, best)
	}
}

// 疊字的上限看音節數：兩顆鍵的 ㄒㄧ 接兩顆鍵的 ㄒㄧ 是兩個單字的疊字（嘻嘻），不是位置差一的單字。
func TestKeyGraphRepeatUsesSyllables(t *testing.T) {
	d, _ := graphFixture()
	keys := graphKeys(zhuyin.ParseReading("ㄒㄧ ㄒㄧ"))
	path, _ := d.Best(keyGraph(keys, true), nil, false)
	plain, _ := d.Best(restrict(keyGraph(keys, true), []int{2, 4}), nil, false)
	if joined(path) != joined(plain) {
		t.Fatalf("graph %q, segmented %q", joined(path), joined(plain))
	}
}

// withCorrected 在每個位置加上其他位置的單鍵假設當成觸控修正（input.Hyp.Corrected，#335），修正的弧多到要另外裁切。
func withCorrected(lat input.Lattice) input.Lattice {
	out := make(input.Lattice, len(lat))
	for i, row := range lat {
		out[i] = slices.Clone(row)
		for j, other := range lat {
			for _, h := range other {
				if j != i && h.Span <= 1 && !slices.ContainsFunc(out[i], func(x input.Hyp) bool { return x.Syl == h.Syl }) {
					out[i] = append(out[i], input.Hyp{Syl: h.Syl, Cost: h.Cost + 3, Corrected: true})
				}
			}
		}
	}
	return out
}

// 逐鍵增量（Search）的修正弧（input.Hyp.Corrected，#335）也另外裁切，跟 Best 相同：打的字機率低、
// 一百多個修正的字機率高但成本更高時，兩邊都選打的字；修正的弧跟打的音搶同一個上限時，打的字會被擠掉。
func TestCorrectedArcsSearchMatchesBest(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	add := func(k int, r zhuyin.Syllable, p float64) string {
		w := string(rune(0x4e00 + k))
		lex.Add(lexicon.Entry{Surface: w, Reading: []zhuyin.Syllable{r}, PRW: 1})
		sys.SetUni(w, p)
		return w
	}
	typed := add(0, "ㄧㄥ", 1e-6)
	untoned := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄧㄥ")})
	lat := input.Lattice{slices.Clone(untoned[0]), slices.Clone(untoned[0])}
	for k := 1; k <= 160; k++ {
		y := zhuyin.Syllable("ㄅㄧㄥ").WithTone(k%5 + 1)
		add(k, y, 1e-2/float64(k))
		for i := range lat {
			if !slices.ContainsFunc(lat[i], func(h input.Hyp) bool { return h.Syl == y }) {
				lat[i] = append(lat[i], input.Hyp{Syl: y, Cost: 10, Corrected: true})
			}
		}
	}
	model := personal.NewModel(lex, sys)
	d := &decode.Decoder{Lex: model.Source(), Score: model}
	want, _ := d.Best(lat, nil, false)
	var s decode.Search
	d.BestSearch(&s, lat[:1], nil, false)
	got, _ := d.BestSearch(&s, lat, nil, false)
	if joined(want) != typed+typed || !reflect.DeepEqual(got, want) {
		t.Fatalf("best %q, search %q, want %q", joined(want), joined(got), typed+typed)
	}
}
