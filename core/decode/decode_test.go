package decode

import (
	"testing"

	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/zhuyin"
)

type uni map[string]float64

func (u uni) Prob(_, w string) float64 { return u[w] }

func fixture() *Decoder {
	lex := lexicon.NewMem()
	score := uni{}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"因", "ㄧㄣ", 8e-4}, {"應", "ㄧㄥ", 4e-4}, {"該", "ㄍㄞ", 6e-4}, {"應該", "ㄧㄥ ㄍㄞ", 2e-3}, {"因為", "ㄧㄣ ㄨㄟˋ", 2e-3}, {"為", "ㄨㄟˋ", 2e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		score[e.w] = e.p
	}
	return &Decoder{Lex: lex, Score: score}
}

func text(path []Seg) string {
	out := ""
	for _, g := range path {
		out += g.Word
	}
	return out
}

// 輸入契約是帶成本的音節格。這裡模擬一個 ㄣ／ㄥ 模糊音前端：
// 每個位置除了使用者打的音節，還有一個帶成本的替代假設。解碼器不需要任何修改。
func TestLatticeWithAlternatives(t *testing.T) {
	d := fixture()
	fuzzy := func(syls ...string) input.Lattice {
		lat := make(input.Lattice, len(syls))
		for i, s := range syls {
			lat[i] = []input.Hyp{{Syl: zhuyin.Syllable(s)}}
			if s == "ㄧㄣ" {
				lat[i] = append(lat[i], input.Hyp{Syl: "ㄧㄥ", Cost: 1.2}) // −ln 0.3
			}
		}
		return lat
	}
	if got := text(d.Decode(fuzzy("ㄧㄣ", "ㄍㄞ"), nil, false)); got != "應該" {
		t.Errorf("模糊音前端：ㄧㄣ ㄍㄞ → %q，應為 應該", got)
	}
	// 精確路徑能成詞時，帶成本的替代假設不得取代它
	if got := text(d.Decode(fuzzy("ㄧㄣ", "ㄨㄟˋ"), nil, false)); got != "因為" {
		t.Errorf("精確輸入被取代：%q", got)
	}
	// 沒有替代假設時照字面解碼
	if got := text(d.Decode(input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄣ ㄍㄞ")), nil, false)); got != "因該" {
		t.Errorf("精確前端：%q", got)
	}
}

func TestLocksAreHardConstraints(t *testing.T) {
	d := fixture()
	lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄧㄥ ㄍㄞ"))
	if got := text(d.Decode(lat, nil, false)); got != "應該" {
		t.Fatalf("無鎖定：%q", got)
	}
	// 鎖定限制的是字，不是斷詞：選了與原詞相同的字，原詞照用（曾把「今天」選「天」後變成「金天」）。
	for _, locks := range []map[int]Lock{{0: {Word: "應", Len: 1}}, {1: {Word: "該", Len: 1}}} {
		if got := text(d.Decode(lat, locks, false)); got != "應該" {
			t.Errorf("鎖定相同的字後文字被改：%v → %q", locks, got)
		}
	}
	// 鎖定的字與原詞不同時，原詞不得跨越它。
	l := nbLex("應", "ㄧㄥ", "鷹", "ㄧㄥ", "該", "ㄍㄞ", "應該", "ㄧㄥ ㄍㄞ")
	d2 := Decoder{Lex: l, Score: uni{"應": .2, "鷹": .1, "該": .2, "應該": .9}}
	if path := d2.Decode(lat, map[int]Lock{0: {Word: "鷹", Len: 1}}, false); text(path) != "鷹該" || len(path) != 2 {
		t.Errorf("不同的鎖定字被原詞蓋掉：%+v", path)
	}
	// 被字面文字切開的暫時邊界仍要求剛好是那個詞。
	if path := d2.Decode(lat, map[int]Lock{0: {Word: "應", Len: 1, LiteralBoundary: 1}}, false); len(path) != 2 {
		t.Errorf("字面邊界的鎖定被合併：%+v", path)
	}
}

func TestEmpty(t *testing.T) {
	if p := fixture().Decode(nil, nil, true); p != nil {
		t.Errorf("空輸入：%+v", p)
	}
}

// MaxCost 界定單一詞的音節格成本：超過上限的讀音不展開，成本 0 的原音路徑不受影響。
func TestMaxCostBoundsSwapsPerWord(t *testing.T) {
	d := fixture()
	lat := input.Lattice{
		{{Syl: "ㄧㄣ"}, {Syl: "ㄧㄥ", Cost: 1.2}},
		{{Syl: "ㄨㄟˋ"}},
	}
	count := func(max float64) (n int) {
		d.MaxCost = max
		d.spans(lat, 0, func(r []zhuyin.Syllable, cost float64) {
			if cost > 0 {
				n++
			}
		})
		return n
	}
	if got := count(0); got == 0 {
		t.Fatal("不設上限時應展開近音讀音")
	}
	if got := count(1.0); got != 0 {
		t.Fatalf("成本上限 1.0 時不應展開成本 1.2 的近音：%d", got)
	}
	if got := count(1.2 + 1e-9); got == 0 {
		t.Fatal("剛好等於一次換音的上限時應展開")
	}
	d.MaxCost = 1.0
	if got := text(d.Decode(lat, nil, false)); got != "因為" {
		t.Fatalf("原音路徑不受上限影響：%q", got)
	}
}

// MaxCandidates 只挑先驗最高的前 N 個詞面進入上下文評分；0 時與原行為完全相同。
func TestMaxCandidatesKeepsTopPriorSurfaces(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	for i, w := range []string{"甲", "乙", "丙", "丁", "戊"} {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄧㄥ"), PRW: 1})
		score[w] = float64(10-i) * 1e-4
	}
	lex.Add(lexicon.Entry{Surface: "甲", Reading: zhuyin.ParseReading("ㄧㄣ"), PRW: 1}) // 同詞面的另一個讀音
	d := &Decoder{Lex: lex, Score: score}
	lat := input.Lattice{{{Syl: "ㄧㄥ"}, {Syl: "ㄧㄣ", Cost: 1.2}}}
	full := d.Candidates(lat, 0, 1, "", nil)
	if len(full) != 5 {
		t.Fatalf("不限時應有 5 個詞面：%v", full)
	}
	d.MaxCandidates = 3
	got := d.Candidates(lat, 0, 1, "", nil)
	var words []string
	for _, c := range got {
		words = append(words, c.Entry.Surface)
	}
	if len(got) != 3 || words[0] != "甲" || words[1] != "乙" || words[2] != "丙" {
		t.Fatalf("應只留先驗最高的 3 個且詞面不重複：%v", words)
	}
	for i := range got {
		if got[i].Score != full[i].Score {
			t.Errorf("入選者的分數不得改變：%v ≠ %v", got[i], full[i])
		}
	}
}

// OnlyCosted 只列舉有成本的讀音：成本 0 的讀音不列出、也不佔 MaxCandidates 名額。
func TestOnlyCostedSkipsTypedReadings(t *testing.T) {
	lex := lexicon.NewMem()
	score := uni{}
	for i, w := range []string{"甲", "乙", "丙", "丁"} {
		lex.Add(lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading("ㄧㄥ"), PRW: 1})
		score[w] = float64(10-i) * 1e-3 // 原音詞面的先驗都比近音高
	}
	lex.Add(lexicon.Entry{Surface: "戊", Reading: zhuyin.ParseReading("ㄧㄣ"), PRW: 1})
	score["戊"] = 1e-6
	d := &Decoder{Lex: lex, Score: score}
	lat := input.Lattice{{{Syl: "ㄧㄥ"}, {Syl: "ㄧㄣ", Cost: 1.2}}}
	got := d.CandidatesInContextWindows(lat, 0, 1, nil,
		Window{Lat: lat},
		Window{Lat: lat, MaxCandidates: 1, OnlyCosted: true})
	if len(got[0]) != 5 {
		t.Fatalf("原音版本應列出全部 5 個：%v", got[0])
	}
	if len(got[1]) != 1 || got[1][0].Entry.Surface != "戊" {
		t.Fatalf("只列有成本讀音時，名額不被原音詞面占掉：%v", got[1])
	}
	if d.OnlyCosted || d.MaxCandidates != 0 {
		t.Fatal("視窗覆寫不得殘留在 Decoder 上")
	}
}

func TestWindowAcceptBeforeBudgetAndOnContextFallback(t *testing.T) {
	for _, brokenContext := range []bool{false, true} {
		lex := lexicon.NewMem()
		lex.Add(lexicon.Entry{Surface: "甲", Reading: zhuyin.ParseReading("ㄧ"), PRW: 1, Correction: true})
		lex.Add(lexicon.Entry{Surface: "乙", Reading: zhuyin.ParseReading("ㄧ"), PRW: 1})
		d := &Decoder{Lex: lex, Score: uni{"甲": .9, "乙": .01}}
		lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄧ"))
		if brokenContext {
			lat = append(lat, []input.Hyp{{Syl: "ㄚ"}})
		}
		got := d.CandidatesInContextWindows(lat, 0, 1, nil,
			Window{Lat: lat, MaxCandidates: 1, Accept: func(e lexicon.Entry) bool { return !e.Correction }},
			Window{Lat: lat, MaxCandidates: 1})
		if len(got[0]) != 1 || got[0][0].Entry.Surface != "乙" || len(got[1]) != 1 || got[1][0].Entry.Surface != "甲" {
			t.Fatalf("fallback=%v, filtered/unfiltered: %+v", brokenContext, got)
		}
	}
}
