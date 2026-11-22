package decode

import (
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聲調選填（#150）：沒打聲調的位置每組（起點、終點）只留機率最高的 maxUntonedArcs 個弧，
// 有打聲調的位置不裁切；鎖定碰到的範圍不裁切，鎖定的字一定還在。
func TestUntonedArcsAreCappedPerSpan(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	tones := []string{"", "ˊ", "ˇ", "ˋ", "˙"}
	for k := 0; k < 3*maxUntonedArcs; k++ {
		w := string(rune(0x4e00 + k))
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄧ" + tones[k%5]), PRW: 1})
		score[w] = 1e-3 / float64(k+1)
	}
	d := &Decoder{Lex: lex, Score: score}
	toned := input.Exact{}.Expand([]zhuyin.Syllable{"ㄧˋ"})
	if n := len(d.wordArcs(toned, 0, nil)); n != 3*maxUntonedArcs/5 {
		t.Fatalf("有打聲調不裁切：%d", n)
	}
	untoned := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄧ")})
	arcs := d.wordArcs(untoned, 0, nil)
	if len(arcs) != maxUntonedArcs {
		t.Fatalf("沒打聲調裁到 %d 個：%d", maxUntonedArcs, len(arcs))
	}
	for k, a := range arcs {
		if want := string(rune(0x4e00 + k)); a.e.Surface != want {
			t.Fatalf("依機率留前面的：第 %d 個是 %q，應為 %q", k, a.e.Surface, want)
		}
	}
	rare := string(rune(0x4e00 + 3*maxUntonedArcs - 1))
	locks := map[int]Lock{0: {Word: rare, Len: 1}}
	if got := text(d.Decode(untoned, locks, false)); got != rare {
		t.Fatalf("鎖定的罕用字不能被裁掉：%q", got)
	}
	if got := text(d.Decode(untoned, nil, false)); got != "一" {
		t.Fatalf("沒鎖定時取機率最高的：%q", got)
	}
}

// decode.Reading 把沒打聲調的音節換回詞條的讀音；有打聲調的位置要相同，詞庫沒有的詞找不到。
func TestReadingResolvesUntonedSyllables(t *testing.T) {
	lex := lexicon.NewMem()
	for _, e := range []struct{ w, r string }{{"會議", "ㄏㄨㄟˋ ㄧˋ"}, {"回憶", "ㄏㄨㄟˊ ㄧˋ"}, {"行", "ㄒㄧㄥˊ"}, {"行", "ㄏㄤˊ"}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
	}
	lat := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄏㄨㄟ"), zhuyin.Untone("ㄧ")})
	for w, want := range map[string]string{"會議": "ㄏㄨㄟˋ ㄧˋ", "回憶": "ㄏㄨㄟˊ ㄧˋ"} {
		if r, ok := Reading(lex, lat, 0, 2, w); !ok || zhuyin.Key(r) != want {
			t.Fatalf("%s：%v %v", w, r, ok)
		}
	}
	if _, ok := Reading(lex, lat, 0, 2, "灰衣"); ok {
		t.Fatal("詞庫沒有的詞")
	}
	mixed := input.Exact{}.Expand([]zhuyin.Syllable{"ㄏㄨㄟˋ", zhuyin.Untone("ㄧ")})
	if _, ok := Reading(lex, mixed, 0, 2, "回憶"); ok {
		t.Fatal("有打的聲調要相同")
	}
	if r, ok := Reading(lex, input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄒㄧㄥ")}), 0, 1, "行"); !ok || r[0] != "ㄒㄧㄥˊ" {
		t.Fatalf("同一個字取符合打的音的讀音：%v %v", r, ok)
	}
}

// 候選評分的右側前後文與整句解碼用同一組弧（#305）：沒打聲調的位置裁掉的罕用字，不能只在候選評分裡
// 讓前面的字排上來。否則候選窗的首選跟組字不同，而且右側每個位置要多算幾百個字，句首的詞長查詢比整句解碼
// 貴一個數量級。
func TestCandidateRightContextUsesDecoderArcs(t *testing.T) {
	lex := lexicon.NewMem()
	score := bigramScore{uni: map[string]float64{"在": .6, "再": .4}, bi: map[[2]string]float64{}}
	for _, w := range []string{"在", "再"} {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄗㄞˋ"), PRW: 1})
	}
	tones := []string{"", "ˊ", "ˇ", "ˋ", "˙"}
	n := 3 * maxUntonedArcs
	for k := 0; k < n; k++ {
		w := string(rune(0x4e00 + k))
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄧ" + tones[k%5]), PRW: 1})
		score.uni[w] = 1e-3 / float64(k+1)
	}
	rare := string(rune(0x4e00 + n - 1)) // 機率最低，整句解碼裁掉
	score.bi[[2]string{"再", rare}] = .9
	d := &Decoder{Lex: lex, Score: score}
	lat := input.Exact{}.Expand([]zhuyin.Syllable{"ㄗㄞˋ", zhuyin.Untone("ㄧ")})
	whole := d.Decode(lat, nil, false)
	if got := text(whole); got != "在一" {
		t.Fatalf("整句解碼：%q", got)
	}
	cands := d.CandidatesInContext(lat, 0, 1, nil, nil)
	if len(cands) < 2 || cands[0].Entry.Surface != "在" {
		t.Fatalf("候選首選要跟組字相同，不能靠整句解碼裁掉的字：%v", cands)
	}
}

type contextualScore struct{ contextScore }

func (s contextualScore) HasContext(left string) bool {
	for k := range s.contextScore {
		if k[0] == left {
			return true
		}
	}
	return false
}

// 右側後綴動態規劃把無上下文的前詞併成一格（#305）：分數與逐一計算完全相同。
func TestCandidateSuffixMergesContextFreeLefts(t *testing.T) {
	lex := lexicon.NewMem()
	for _, x := range []struct{ w, r string }{
		{"在", "ㄗㄞˋ"}, {"再", "ㄗㄞˋ"}, {"載", "ㄗㄞˋ"}, {"見", "ㄐㄧㄢˋ"}, {"建", "ㄐㄧㄢˋ"}, {"面", "ㄇㄧㄢˋ"}, {"見面", "ㄐㄧㄢˋ ㄇㄧㄢˋ"},
	} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
	}
	score := contextScore{{"<s>", "在"}: 0.5, {"再", "見"}: 0.8, {"見", "面"}: 0.3, {"載", "見面"}: 0.2}
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄗㄞˋ ㄐㄧㄢˋ ㄇㄧㄢˋ"))
	plain := (&Decoder{Lex: lex, Score: score}).CandidatesInContext(lat, 0, 1, nil, nil)
	merged := (&Decoder{Lex: lex, Score: contextualScore{score}}).CandidatesInContext(lat, 0, 1, nil, nil)
	if len(plain) != 3 || len(merged) != len(plain) {
		t.Fatalf("候選數：%v %v", plain, merged)
	}
	for i := range plain {
		if plain[i].Entry.Surface != merged[i].Entry.Surface || plain[i].Score != merged[i].Score {
			t.Fatalf("第 %d 個不同：%v %v", i, plain[i], merged[i])
		}
	}
}

// 桌面的聲調選填加模糊音（#150）：沒打聲調的 ㄓㄣ 展開成 5 個聲調 × 4 個近音（ㄓㄣ ㄗㄣ ㄓㄥ ㄗㄥ），一列 20 個假設，
// 比 continuationRow 多，但它不是簡拼：弧數上限仍是 maxUntonedArcs，留下哪些弧照無上下文機率乘讀音權重
// （同 main，不另乘近音成本）。簡拼要看音節格的明確標記，不能從列長推斷。
func TestTonelessFuzzyRowIsNotAbbreviated(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	tones := []string{"", "ˊ", "ˇ", "ˋ", "˙"}
	n := 0
	for _, b := range []string{"ㄓㄣ", "ㄗㄣ", "ㄓㄥ", "ㄗㄥ"} {
		for k := range 30 {
			w := string(rune(0x4e00 + n))
			lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(b + tones[k%5]), PRW: 1})
			score[w] = 1e-3 / float64(n%37+1) // 打的音與近音的機率交錯：裁切看機率，不看成本
			n++
		}
	}
	fz := fuzzy.Config{}.With(fuzzy.ZZh, fuzzy.Select).With(fuzzy.EnEng, fuzzy.Select)
	lat := fuzzy.Frontend{Config: fz}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄓㄣ")})
	if len(lat[0]) != 20 {
		t.Fatalf("expected 20 hypotheses, got %d", len(lat[0]))
	}
	if abbreviatedSpan(lat, 0, 1) {
		t.Fatal("a toneless syllable with near sounds is not an abbreviation")
	}
	d := &Decoder{Lex: lex, Score: score}
	arcs := d.wordArcs(lat, 0, nil)
	if len(arcs) != maxUntonedArcs {
		t.Fatalf("kept %d arcs, want maxUntonedArcs (%d)", len(arcs), maxUntonedArcs)
	}
	// main 的做法：所有弧依 Prob×PRW 穩定排序（依列舉順序），取前 maxUntonedArcs 個。
	var all []arc
	for _, h := range lat[0] {
		for _, e := range lex.Exact([]zhuyin.Syllable{h.Syl}) {
			all = append(all, arc{1, e, h.Cost})
		}
	}
	sort.SliceStable(all, func(a, b int) bool {
		return score[all[a].e.Surface]*all[a].e.PRW > score[all[b].e.Surface]*all[b].e.PRW
	})
	var want, got []string
	for _, a := range all[:maxUntonedArcs] {
		want = append(want, a.e.Surface)
	}
	for _, a := range arcs {
		got = append(got, a.e.Surface)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("kept arcs differ from main:\ngot  %v\nwant %v", got, want)
	}
}

// 簡拼位置走簡拼的弧數上限，跟展開成幾個假設無關（打了聲調的 ㄈˇ 只有十個左右）。
func TestAbbreviatedRowIsMarked(t *testing.T) {
	lat := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Abbreviate("ㄈ", 3), "ㄏㄠˇ"})
	if len(lat[0]) > continuationRow {
		t.Fatalf("test needs a short abbreviated row, got %d", len(lat[0]))
	}
	if !abbreviatedSpan(lat, 0, 1) || !abbreviatedSpan(lat, 0, 2) || abbreviatedSpan(lat, 1, 2) {
		t.Fatal("an abbreviated span follows the abbreviated syllable, not the row length")
	}
}

// hidePrefixer 藏起詞庫的 lexicon.Continuer，讓 spans 只能逐一問 HasPrefix。
type hidePrefixer struct{ lexicon.Source }

// 寬列改用 lexicon.Continuations 剪枝只是換查法：列舉出的讀音與成本跟逐一問 HasPrefix 一樣。
func TestWideRowContinuationsMatchHasPrefix(t *testing.T) {
	lex := lexicon.NewMem()
	for _, r := range []string{"ㄓㄣˋ ㄉㄜ˙", "ㄓㄥˋ ㄗㄞˋ", "ㄗㄣˇ ㄇㄜ˙", "ㄓㄣ ㄕˊ", "ㄗㄥ ㄐㄧㄚ", "ㄓㄣ", "ㄉㄜ˙", "ㄉㄜˊ ㄓㄥˋ"} {
		lex.Add(lexicon.Entry{Surface: r, Reading: zhuyin.ParseReading(r), PRW: 1})
	}
	fz := fuzzy.Config{}.With(fuzzy.ZZh, fuzzy.Select).With(fuzzy.EnEng, fuzzy.Select)
	lat := fuzzy.Frontend{Config: fz}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄉㄜ"), zhuyin.Untone("ㄓㄣ"), zhuyin.Untone("ㄉㄜ")})
	collect := func(src lexicon.Source) []string {
		var out []string
		d := &Decoder{Lex: src, Score: uni{}}
		for at := range lat {
			d.spans(lat, at, func(r []zhuyin.Syllable, cost float64) {
				out = append(out, fmt.Sprintf("%d:%s/%g", at, zhuyin.Key(r), cost))
			})
		}
		return out
	}
	fast, slow := collect(lex), collect(hidePrefixer{lex})
	if len(fast) == 0 || !slices.Equal(fast, slow) {
		t.Fatalf("Continuations %v\nHasPrefix     %v", fast, slow)
	}
}

// 沒打聲調的位置，觸控修正（input.Hyp.Corrected，#335）的弧另外依成本留前 maxCorrectedArcs 個，不跟打的音搶
// maxUntonedArcs：修正的讀音機率再高，也不能把打的音裡機率最低的字擠掉。打了聲調的位置照舊不裁切。
func TestCorrectedArcsDoNotCrowdTypedOnes(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	add := func(k int, r string, p float64) string {
		w := string(rune(0x4e00 + k))
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: 1})
		score[w] = p
		return w
	}
	typed := add(0, "ㄧㄥ", 1e-6) // 鶯：打的音裡唯一的字，機率很低
	lat := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄧㄥ")})
	for k := 1; k <= 2*maxUntonedArcs; k++ {
		y := zhuyin.Syllable("ㄅㄧㄥ").WithTone(k%5 + 1)
		add(k, string(y), 1e-2/float64(k))
		if !slices.ContainsFunc(lat[0], func(h input.Hyp) bool { return h.Syl == y }) {
			lat[0] = append(lat[0], input.Hyp{Syl: y, Cost: 5, Corrected: true})
		}
	}
	arcs := d0(lex, score).wordArcs(lat, 0, nil)
	if !slices.ContainsFunc(arcs, func(a arc) bool { return a.e.Surface == typed }) {
		t.Fatalf("打的音被修正擠掉")
	}
	fixed := 0
	for _, a := range arcs {
		if a.e.Surface != typed {
			fixed++
		}
	}
	if fixed != maxCorrectedArcs {
		t.Fatalf("修正的弧留 %d 個，應為 %d", fixed, maxCorrectedArcs)
	}
	// 打了聲調：修正的弧全部留著。
	toned := input.Lattice{{{Syl: "ㄧㄥ"}}}
	for _, h := range lat[0] {
		if h.Corrected {
			toned[0] = append(toned[0], h)
		}
	}
	if got := len(d0(lex, score).wordArcs(toned, 0, nil)); got != 1+2*maxUntonedArcs {
		t.Fatalf("toned arcs %d, want all %d", got, 1+2*maxUntonedArcs)
	}
	// 沒有修正的假設時（桌面）一切照舊。
	plain := input.Exact{}.Expand([]zhuyin.Syllable{zhuyin.Untone("ㄧㄥ")})
	if got := len(d0(lex, score).wordArcs(plain, 0, nil)); got != 1 {
		t.Fatalf("plain arcs %d", got)
	}
}

func d0(lex lexicon.Source, score uni) *Decoder { return &Decoder{Lex: lex, Score: score} }
