package decode

import (
	"math"
	"testing"

	"git.biggo.com/0c/ime/core/input"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// bigramScore 以 0.7 內插系統 bigram 與 unigram，同 personal.Model 沒有個人資料時的 SystemProb。
type bigramScore struct {
	uni      map[string]float64
	bi       map[[2]string]float64
	observed map[[2]string]bool
}

func (b bigramScore) Prob(left, w string) float64 {
	return .7*b.bi[[2]string{left, w}] + .3*b.uni[w]
}

// SystemBigram 是內插裡系統 bigram 實際觀察到的部分（decode.SystemBigram）。
func (b bigramScore) SystemBigram(left, w string) float64 { return .7 * b.bi[[2]string{left, w}] }

type observedScore struct{ bigramScore }

func (o observedScore) ObservedPair(left, w string) bool { return o.observed[[2]string{left, w}] }

// enFixture 取 dev.62 資料包的實際數值（#139）：「恩」單字頻率與 恩→恩 的 bigram 都高，
// 詞庫裡的「嗯嗯」只有補足詞頻；另有 AABB 成語「恩恩怨怨」與「是／試」這組同因的疊字。
func enFixture() (*lexicon.Mem, bigramScore) {
	lex := lexicon.NewMem()
	b := bigramScore{uni: map[string]float64{}, bi: map[[2]string]float64{}}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"恩", "ㄣ", 5.1e-5}, {"嗯", "ㄣ", 5e-5}, {"嗯嗯", "ㄣ ㄣ", 5.3e-8},
		{"怨", "ㄩㄢˋ", 3e-5}, {"恩恩怨怨", "ㄣ ㄣ ㄩㄢˋ ㄩㄢˋ", 6.9e-7},
		{"是", "ㄕˋ", 1.08e-2}, {"試", "ㄕˋ", 3.4e-5}, {"試試", "ㄕˋ ㄕˋ", 2.9e-5},
		{"看", "ㄎㄢˋ", 6e-4}, {"看看", "ㄎㄢˋ ㄎㄢˋ", 3e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		b.uni[e.w] = e.p
	}
	b.bi[[2]string{lm.BOS, "恩"}] = 3e-5
	b.bi[[2]string{"恩", "恩"}] = 5.9e-3
	b.bi[[2]string{"嗯", "恩"}] = 2e-4
	b.bi[[2]string{"是", "是"}] = 3e-4
	b.bi[[2]string{"看", "看"}] = 5e-2
	return lex, b
}

func enLat(r string) input.Lattice { return input.Exact{}.Expand(zhuyin.ParseReading(r)) }

func TestRepeatedSinglesDoNotBeatLexiconWord(t *testing.T) {
	lex, b := enFixture()
	d := &Decoder{Lex: lex, Score: b, Continue: .5, SeparateContinuation: true}
	for _, c := range []struct {
		reading, want string
	}{
		{"ㄣ ㄣ", "嗯嗯"},     // 兩個「恩」拼出的非詞疊字，曾贏過詞庫的「嗯嗯」
		{"ㄕˋ ㄕˋ", "試試"},   // 同因：「是」單字頻率高，內插退回就讓「是是」勝出
		{"ㄎㄢˋ ㄎㄢˋ", "看看"}, // 疊字本身是詞：照常
		{"ㄣ ㄣ ㄩㄢˋ ㄩㄢˋ", "恩恩怨怨"},
	} {
		for _, prefix := range []bool{false, true} {
			if got := text(d.Decode(enLat(c.reading), nil, prefix)); got != c.want {
				t.Errorf("%s prefix=%v → %q，應為 %q", c.reading, prefix, got, c.want)
			}
		}
	}
	// 「恩恩怨怨」只打到前兩個音節時，尾端前綴「恩恩」本身也不是詞，不能蓋過已打完的「嗯嗯」。
	// 拿掉 恩→恩 的 bigram，讓勝出的只可能是成語前綴。
	delete(b.bi, [2]string{"恩", "恩"})
	if got := d.Decode(enLat("ㄣ ㄣ"), nil, true); text(got) != "嗯嗯" {
		t.Errorf("AABB 前綴：%+v", got)
	}
	b.bi[[2]string{"恩", "恩"}] = 5.9e-3
	// 非詞疊字仍留在 N-best 裡，只是排在詞後面。
	rows, _ := d.NBest(enLat("ㄣ ㄣ"), nil, true, 4)
	if len(rows) < 2 || rows[0].Text != "嗯嗯" || rows[1].Text != "恩恩" {
		t.Errorf("N-best：%+v", rows)
	}
	if !rows[1].NonWordRepeat || rows[0].NonWordRepeat {
		t.Errorf("NonWordRepeat：%+v", rows[:2])
	}
	if a := d.AuditTarget(enLat("ㄣ ㄣ"), nil, true, "恩恩"); a.Best == nil || a.Best.LogScore != rows[1].LogScore {
		t.Errorf("AuditTarget 與 N-best 分數不一致：%+v", a.Best)
	}
}

func TestRepeatedSinglesFollowLocksAndPersonalUse(t *testing.T) {
	lex, b := enFixture()
	d := &Decoder{Lex: lex, Score: b}
	// 使用者在第二個音節選了「恩」：「嗯嗯」不能用了，不再壓低疊字（否則會變成「嗯恩」）。
	if got := text(d.Decode(enLat("ㄣ ㄣ"), map[int]Lock{1: {Word: "恩", Len: 1}}, false)); got != "恩恩" {
		t.Errorf("鎖定第二字：%q", got)
	}
	// 使用者提交過「恩」後接「恩」：照個人用法，不受上限約束。
	b.observed = map[[2]string]bool{{"恩", "恩"}: true}
	d.Score = observedScore{b}
	if got := text(d.Decode(enLat("ㄣ ㄣ"), nil, true)); got != "恩恩" {
		t.Errorf("個人用過的疊字：%q", got)
	}
}

// priorScore 另有不含前文的先驗（系統 unigram），同 personal.Model 的 Prior。
type priorScore struct{ bigramScore }

func (p priorScore) Prior(w string) float64 { return p.uni[w] }

// repeatFixture 以一段讀音的單字與詞（讀音、unigram、讀音權重）和 bigram 建詞庫與評分器。
func repeatFixture(entries []repeatEntry, bi map[[2]string]float64) (*lexicon.Mem, priorScore) {
	lex := lexicon.NewMem()
	b := bigramScore{uni: map[string]float64{}, bi: bi}
	for _, e := range entries {
		prw := e.prw
		if prw == 0 {
			prw = 1
		}
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: prw})
		b.uni[e.w] = e.p
	}
	return lex, priorScore{b}
}

type repeatEntry struct {
	w, r   string
	p, prw float64
}

// #140：詞庫詞只有補足詞頻、或由罕用字組成時，上限曾把語料常寫的疊字壓到詞庫詞以下，結果讓給
// 既不是詞、也不是疊字的組合（柯科、倪妮），或罕用的詞（嘓嘓）。數值取自 dev.63 資料包（科科還沒收進詞庫）。
func TestRepeatCapLeavesRepeatsTheLexiconWordCannotBeat(t *testing.T) {
	for _, c := range []struct {
		name, reading, want string
		entries             []repeatEntry
		bi                  map[[2]string]float64
	}{
		{
			// 磕磕只有補足詞頻；另一個疊字「顆顆」與「柯科」都勝過它，壓低科科也不會讓磕磕勝出。
			name: "科科", reading: "ㄎㄜ ㄎㄜ", want: "科科",
			entries: []repeatEntry{{w: "科", r: "ㄎㄜ", p: 1.34e-4}, {w: "柯", r: "ㄎㄜ", p: 6.91e-5}, {w: "顆", r: "ㄎㄜ", p: 7.55e-5},
				{w: "磕", r: "ㄎㄜ", p: 2.28e-6}, {w: "磕磕", r: "ㄎㄜ ㄎㄜ", p: 5.3e-8}},
			bi: map[[2]string]float64{{"科", "科"}: 6.37e-3, {"柯", "科"}: 9.73e-4, {"柯", "柯"}: 1.86e-3, {"顆", "顆"}: 9.82e-3, {"磕", "磕"}: .115},
		},
		{
			// 呢呢輸給另一個疊字「尼尼」（「呢」在 ㄋㄧˊ 只有一成讀音權重）；兩個疊字都被壓低時曾變成「倪妮」。
			name: "尼尼", reading: "ㄋㄧˊ ㄋㄧˊ", want: "尼尼",
			entries: []repeatEntry{{w: "尼", r: "ㄋㄧˊ", p: 1.07e-4}, {w: "妮", r: "ㄋㄧˊ", p: 2.7e-5}, {w: "呢", r: "ㄋㄧˊ", p: 7.85e-4, prw: .1},
				{w: "倪", r: "ㄋㄧˊ", p: 6.83e-6}, {w: "泥", r: "ㄋㄧˊ", p: 2.64e-5}, {w: "呢呢", r: "ㄋㄧˊ ㄋㄧˊ", p: 5.3e-8}},
			bi: map[[2]string]float64{{"尼", "尼"}: 1.06e-2, {"妮", "妮"}: 3.6e-2, {"倪", "妮"}: 3.35e-2, {"倪", "倪"}: 3.32e-3,
				{"泥", "泥"}: 8.42e-4, {"呢", "呢"}: 1.2e-4},
		},
		{
			// 嘓嘓勝過其他兩個單字的讀法，但「嘓」比「郭」罕用得多：郭→郭 實際觀察到的 bigram 照算。
			name: "郭郭", reading: "ㄍㄨㄛ ㄍㄨㄛ", want: "郭郭",
			entries: []repeatEntry{{w: "郭", r: "ㄍㄨㄛ", p: 1.23e-4}, {w: "鍋", r: "ㄍㄨㄛ", p: 1.7e-5}, {w: "嘓", r: "ㄍㄨㄛ", p: 1.59e-7},
				{w: "嘓嘓", r: "ㄍㄨㄛ ㄍㄨㄛ", p: 5.3e-8}},
			bi: map[[2]string]float64{{"郭", "郭"}: 1.4e-3, {"鍋", "鍋"}: 1.3e-3},
		},
	} {
		lex, sc := repeatFixture(c.entries, c.bi)
		d := &Decoder{Lex: lex, Score: sc, Continue: .5, SeparateContinuation: true}
		for _, prefix := range []bool{false, true} {
			if got := text(d.Decode(enLat(c.reading), nil, prefix)); got != c.want {
				t.Errorf("%s prefix=%v → %q，應為 %q", c.name, prefix, got, c.want)
			}
		}
		// 沒被上限壓過的疊字不能被神經選字的否決擋掉。
		rows, _ := d.NBest(enLat(c.reading), nil, true, 8)
		if len(rows) == 0 || rows[0].Text != c.want || rows[0].NonWordRepeat {
			t.Errorf("%s N-best：%+v", c.name, rows)
		}
	}
}

// #140 必須守住 #139：詞庫詞的字跟 X 差不多常用（嗯嗯對恩恩）時照樣壓到詞庫詞以下；X 常用得多
// （是是對試試）時，只剩語料實際觀察到的 是→是 也輸給試試，同樣被壓低並標出。
func TestRepeatCapStillHoldsForCommonLexiconWords(t *testing.T) {
	lex, b := enFixture()
	d := &Decoder{Lex: lex, Score: priorScore{b}, Continue: .5, SeparateContinuation: true}
	for _, c := range []struct{ reading, want, repeat string }{{"ㄣ ㄣ", "嗯嗯", "恩恩"}, {"ㄕˋ ㄕˋ", "試試", "是是"}} {
		rows, _ := d.NBest(enLat(c.reading), nil, true, 8)
		if len(rows) == 0 || rows[0].Text != c.want {
			t.Fatalf("%s：%+v", c.reading, rows)
		}
		for _, r := range rows {
			if r.Text == c.repeat && !r.NonWordRepeat {
				t.Errorf("%s 的 %s 應標為被上限壓過：%+v", c.reading, c.repeat, r)
			}
		}
	}
}

// 0.2.0 詞庫（docs/49 第 6 步）讓「嗯嗯」本身的詞頻夠高，不必壓分就勝過恩恩；疊字沒被壓過，一度不再標記，
// 神經選字又把它換回恩恩。疊字原本就在上限以下時，詞庫詞兩字相黏的程度明顯高過 X→X 才標記：
// 嗯嗯比恩恩更像一個詞；每每勝過美美只是因為「每」比「美」常用，美美照常交給神經選字。
// 數值取自 0.2.0 資料包（850a075）。
func TestRepeatBelowCapStaysFlaggedWhenTheWordIsMoreLexical(t *testing.T) {
	for _, c := range []struct {
		reading, want, repeat string
		flagged               bool
		entries               []repeatEntry
		bi                    map[[2]string]float64
	}{
		{
			reading: "ㄣ ㄣ", want: "嗯嗯", repeat: "恩恩", flagged: true,
			entries: []repeatEntry{{w: "恩", r: "ㄣ", p: 8.66e-5}, {w: "嗯", r: "ㄣ", p: 3.98e-5}, {w: "嗯嗯", r: "ㄣ ㄣ", p: 1.72e-6}},
			bi:      map[[2]string]float64{{"恩", "恩"}: 5.13e-3},
		},
		{
			reading: "ㄇㄟˇ ㄇㄟˇ", want: "每每", repeat: "美美", flagged: false,
			entries: []repeatEntry{{w: "美", r: "ㄇㄟˇ", p: 4.6e-4}, {w: "每", r: "ㄇㄟˇ", p: 6.38e-4}, {w: "每每", r: "ㄇㄟˇ ㄇㄟˇ", p: 7.21e-6}},
			bi:      map[[2]string]float64{{"美", "美"}: 6.37e-3},
		},
	} {
		lex, sc := repeatFixture(c.entries, c.bi)
		d := &Decoder{Lex: lex, Score: sc, Continue: .5, SeparateContinuation: true}
		rows, _ := d.NBest(enLat(c.reading), nil, true, 8)
		if len(rows) == 0 || rows[0].Text != c.want || rows[0].NonWordRepeat {
			t.Fatalf("%s：%+v", c.reading, rows)
		}
		x, found := c.repeat[:len(c.repeat)/2], false
		for _, r := range rows {
			if r.Text != c.repeat {
				continue
			}
			found = true
			// 疊字原本就在上限以下：分數是兩個單字的原分數，沒有被壓。
			if want := math.Log(sc.Prob(lm.BOS, x)) + math.Log(sc.Prob(x, x)); math.Abs(r.LogScore-want) > 1e-9 {
				t.Errorf("%s 的 %s 分數 %v，應為未壓分的 %v", c.reading, c.repeat, r.LogScore, want)
			}
			if r.NonWordRepeat != c.flagged {
				t.Errorf("%s 的 %s NonWordRepeat=%v，應為 %v", c.reading, c.repeat, r.NonWordRepeat, c.flagged)
			}
		}
		if !found {
			t.Errorf("%s：N-best 沒有 %s：%+v", c.reading, c.repeat, rows)
		}
	}
}
