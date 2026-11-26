package english

import (
	"reflect"
	"slices"
	"testing"
)

func rankedFixture(t *testing.T) *Dictionary {
	t.Helper()
	d, err := NewRanked([]Entry{
		{Text: "the", Weight: 100}, {Text: "ten", Weight: 72}, {Text: "tea", Weight: 70},
		{Text: "hello", Weight: 74}, {Text: "help", Weight: 80}, {Text: "hell", Weight: 69},
		{Text: "which", Weight: 85}, {Text: "with", Weight: 91}, {Text: "rich", Weight: 70},
		{Text: "don't", Weight: 80}, {Text: "done", Weight: 76}, {Text: "dot", Weight: 70},
		{Text: "i", Display: "I", Weight: 92}, {Text: "i'm", Display: "I'm", Weight: 79},
		{Text: "its", Weight: 84}, {Text: "it's", Weight: 83}, {Text: "ill", Weight: 60}, {Text: "i'll", Display: "I'll", Weight: 71},
		{Text: "iphone", Display: "iPhone", Weight: 86, Protected: true}, {Text: "line", Display: "LINE", Weight: 89, Protected: true},
		{Text: "xd", Display: "XD", Weight: 94, Protected: true},
		{Text: "good", Weight: 79}, {Text: "home", Weight: 83}, {Text: "gone", Weight: 73},
		{Text: "shit", Weight: 70, NoSuggest: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func texts(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

func TestRankedCorrectionsUseTypoCostAndFrequency(t *testing.T) {
	d := rankedFixture(t)
	for typed, want := range map[string]string{
		"teh":  "the",   // 顛倒，詞頻最高
		"helo": "hello", // 少打重複字母，勝過相鄰鍵打錯的 help（詞頻較高）
		"wich": "which", // 漏打一個字母，勝過不相鄰鍵打錯的 with、rich
		"dont": "don't", // 漏打撇號
		"im":   "I'm",   // 兩個字母只修縮寫
		"Dont": "Don't",
		"DONT": "DON'T",
	} {
		if got := d.Corrections(typed, 3); len(got) == 0 || got[0].Text != want {
			t.Errorf("%s: %v", typed, texts(got))
		}
	}
	for _, typed := range []string{"its", "ill", "the", "XD", "iphone", "don't", "ab"} {
		if got := d.Corrections(typed, 3); got != nil {
			t.Errorf("%s 是字（或太短），不能修正：%v", typed, texts(got))
		}
	}
	// 本身是字的無撇號拼法只當建議，由外殼決定不自動修正。
	if got := texts(d.Contractions("its")); !slices.Equal(got, []string{"it's"}) {
		t.Fatal(got)
	}
	if got := texts(d.Contractions("ill")); !slices.Equal(got, []string{"I'll"}) {
		t.Fatal(got)
	}
	if d.Contractions("the") != nil || d.Contractions("don't") != nil {
		t.Fatal("contractions only for apostrophe-free spellings")
	}
}

func TestRankedDisplayAndProtection(t *testing.T) {
	d := rankedFixture(t)
	for typed, want := range map[string]string{
		"i": "I", "iphone": "iPhone", "Iphone": "iPhone", "IPHONE": "IPHONE", "line": "LINE", "Line": "LINE",
		"the": "the", "The": "The", "THE": "THE", "i'm": "I'm", "xd": "XD", "nope": "",
	} {
		if got := d.Display(typed); got != want {
			t.Errorf("%s: %q want %q", typed, got, want)
		}
	}
	if got := texts(d.Prefix("iph", 3)); !slices.Equal(got, []string{"iPhone"}) {
		t.Fatal(got)
	}
	if got := texts(d.Prefix("do", 3)); !slices.Equal(got, []string{"don't", "done", "dot"}) {
		t.Fatal(got)
	}
	if !d.Protected("XD") || !d.Protected("iphone") || d.Protected("the") || d.Protected("nope") {
		t.Fatal("protected")
	}
	if !d.Has("Don't") || d.Has("dont") || d.Suggestible("shit") || !d.Has("shit") {
		t.Fatal("lookup")
	}
	if d.Prior("the") != 100 || d.Prior("nope") != 0 {
		t.Fatal("prior")
	}
}

func TestT9(t *testing.T) {
	d := rankedFixture(t)
	if got := texts(d.T9("4663", 5)); !slices.Equal(got, []string{"home", "good", "gone"}) {
		t.Fatal(got)
	}
	if got := texts(d.T9("4", 5)); !slices.Equal(got, []string{"I"}) {
		t.Fatal(got)
	}
	if got := texts(d.T9("416", 5)); !slices.Equal(got, []string{"I'm"}) {
		t.Fatal(got)
	}
	if d.T9("40", 5) != nil || d.T9("", 5) != nil || d.T9("7448", 5) != nil {
		t.Fatal("invalid digits or no-suggest words")
	}
}

func TestRankedRoundTripAndRejects(t *testing.T) {
	d := rankedFixture(t)
	back, err := Decode(d.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if !back.Ranked() || !reflect.DeepEqual(back, d) {
		t.Fatal("format 2 round trip")
	}
	legacy := New([]Word{{"hello", 3}})
	if back, err := Decode(legacy.Encode()); err != nil || back.Ranked() || back.Display("hello") != "hello" {
		t.Fatal("format 1 stays format 1")
	}
	for _, bad := range [][]Entry{
		{{Text: "Hello", Weight: 1}}, {{Text: "'em", Weight: 1}}, {{Text: "don''t", Weight: 1}}, {{Text: "ab", Weight: 0}},
		{{Text: "ab", Display: "cd", Weight: 1}}, {{Text: "ab", Display: "ab", Weight: 1}}, {{Text: "ab", Weight: 1}, {Text: "ab", Weight: 2}},
		{{Text: "t1", Weight: 1}},
	} {
		if _, err := NewRanked(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	data := d.Encode()
	if _, err := Decode(data[:len(data)-1]); err == nil {
		t.Fatal("accepted truncated pack")
	}
}

func TestTypoCost(t *testing.T) {
	for _, tc := range []struct {
		typed, word string
		want        float64
	}{
		{"helo", "hello", doubledCost}, {"helllo", "hello", doubledCost}, {"dont", "don't", apostropheCost},
		{"helo", "help", editCost}, {"wich", "with", farCost}, {"wich", "which", editCost}, {"teh", "the", editCost},
		{"the", "the", 0}, {"THE", "the", 0},
	} {
		if got := TypoCost(tc.typed, tc.word); got != tc.want {
			t.Errorf("%s→%s: %v want %v", tc.typed, tc.word, got, tc.want)
		}
	}
}

func TestBigrams(t *testing.T) {
	b, err := NewBigrams([3][]Pair{
		EnglishEnglish: {{"of", "the", 30}, {"of", "a", 6}, {"in", "the", 29}},
		ChineseEnglish: {{"笑死", "xd", 83}, {"我", "ok", 80}},
		EnglishChinese: {{"ai", "泡沫", 101}, {"ai", "晶片", 50}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.After(EnglishEnglish, "Of", 5); !reflect.DeepEqual(got, []Next{{"the", 30}, {"a", 6}}) {
		t.Fatal(got)
	}
	if b.Count(ChineseEnglish, "笑死", "XD") != 83 || b.Count(EnglishChinese, "AI", "晶片") != 50 || b.Count(EnglishEnglish, "the", "of") != 0 {
		t.Fatal("count")
	}
	back, err := DecodeBigrams(b.Encode())
	if err != nil || !reflect.DeepEqual(back, b) {
		t.Fatalf("round trip: %v", err)
	}
	for _, bad := range [][3][]Pair{
		{EnglishEnglish: {{"of", "the", 0}}}, {EnglishEnglish: {{"Of", "the", 1}}}, {ChineseEnglish: {{"ok", "xd", 1}}},
		{EnglishChinese: {{"ai", "ai", 1}}}, {EnglishEnglish: {{"of", "the", 1}, {"of", "the", 2}}},
	} {
		if _, err := NewBigrams(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	data := b.Encode()
	if _, err := DecodeBigrams(data[:len(data)-2]); err == nil {
		t.Fatal("accepted truncated pairs")
	}
}

// #358：打的字母裡有第一個以外的大寫（DeepS、PCm）時照打的寫法往後接，不換成句首大寫。
func TestRankedKeepsTypedInnerCapitals(t *testing.T) {
	d, err := NewRanked([]Entry{
		{Text: "deepseek", Weight: 70}, {Text: "pcman", Weight: 60}, {Text: "iphone", Display: "iPhone", Weight: 86},
		{Text: "openai", Display: "OpenAI", Weight: 75}, {Text: "hello", Weight: 74},
	})
	if err != nil {
		t.Fatal(err)
	}
	for typed, want := range map[string]string{
		"DeepS": "DeepSeek", "PCm": "PCman", "deeps": "deepseek", "Deeps": "Deepseek", "DEEPS": "DEEPSEEK",
		"iph": "iPhone", "Iph": "iPhone", "iPh": "iPhone", "OpenA": "OpenAI", "open": "OpenAI", "hEl": "hEllo",
	} {
		if got := texts(d.Prefix(typed, 1)); len(got) != 1 || got[0] != want {
			t.Errorf("Prefix(%s) = %v，要 %s", typed, got, want)
		}
	}
	for typed, want := range map[string]string{"DeepSeek": "DeepSeek", "deepseek": "deepseek", "PCMan": "PCMan", "iphone": "iPhone"} {
		if got := d.Display(typed); got != want {
			t.Errorf("Display(%s) = %q，要 %q", typed, got, want)
		}
	}
	// 修正的字跟打的不對齊（少打、多打），不照位置套大小寫。
	if got := texts(d.Corrections("DeepSek", 1)); len(got) != 1 || got[0] != "Deepseek" {
		t.Fatal(got)
	}
}

// #358：混打的中英判斷不認語料新加的三個字母以內的詞（da、tv、ae），補字與修正仍用完整字典。
func TestMixedViewHidesShortCorpusWords(t *testing.T) {
	d, err := NewRanked([]Entry{
		{Text: "da", Weight: 70, CorpusOnly: true}, {Text: "dan", Weight: 60}, {Text: "data", Weight: 80},
		{Text: "tv", Display: "TV", Weight: 75, CorpusOnly: true}, {Text: "tvbs", Display: "TVBS", Weight: 60, CorpusOnly: true},
		{Text: "ten", Weight: 72}, {Text: "tea", Weight: 70}, {Text: "gdp", Display: "GDP", Weight: 76, CorpusOnly: true},
		{Text: "i'm", Display: "I'm", Weight: 79, CorpusOnly: true}, {Text: "im", Weight: 50, CorpusOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := d.Mixed()
	if m == d || m.Mixed() != m || !m.Ranked() || m.Len() != d.Len() {
		t.Fatal("mixed view")
	}
	for _, w := range []string{"da", "tv", "TV", "gdp", "im"} {
		if !d.Has(w) || m.Has(w) || m.Suggestible(w) || m.Display(w) != "" || m.Prior(w) != 0 {
			t.Errorf("%s：完整字典要認、混打不認", w)
		}
	}
	for _, w := range []string{"dan", "data", "tvbs", "ten", "i'm"} {
		if !m.Has(w) {
			t.Errorf("%s：不是三個字母以內的語料新詞，混打照認", w)
		}
	}
	if got := texts(m.Prefix("t", 5)); !slices.Equal(got, []string{"ten", "tea", "TVBS"}) {
		t.Fatal(got)
	}
	if got := texts(d.Prefix("t", 5)); !slices.Equal(got, []string{"TV", "ten", "tea", "TVBS"}) {
		t.Fatal(got)
	}
	if got := texts(m.Prefix("da", 5)); !slices.Equal(got, []string{"data", "dan"}) {
		t.Fatal(got)
	}
	if got := m.Prefix("gd", 5); len(got) != 0 {
		t.Fatal(texts(got))
	}
	if got := texts(m.Corrections("tvx", 5)); slices.Contains(got, "TV") {
		t.Fatal(got)
	}
	if got := texts(d.Corrections("tvx", 5)); !slices.Contains(got, "TV") {
		t.Fatal(got)
	}
	if got := texts(m.Contractions("im")); !slices.Equal(got, []string{"I'm"}) {
		t.Fatal(got)
	}
	// 往返保留語料新詞的標記；格式 1 與沒有短新詞的字典，混打就是同一份。
	back, err := Decode(d.Encode())
	if err != nil || back.Mixed().Has("tv") || !back.Has("tv") {
		t.Fatalf("round trip: %v", err)
	}
	if legacy := New([]Word{{"hello", 3}}); legacy.Mixed() != legacy {
		t.Fatal("format 1")
	}
	if plain := rankedFixture(t); plain.Mixed() != plain {
		t.Fatal("no short corpus words")
	}
	var none *Dictionary
	if none.Mixed() != nil {
		t.Fatal("nil")
	}
}
