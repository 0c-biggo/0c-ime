package compile

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/zhuyin"
)

// rareSources 與 cmd/0c-compile 的預設相同（出貨字典也用這組，見 scripts/build_release_data.py）。
const rareSources = "cns11643,mcbopomofo-rare"

func rareOf(t *testing.T, res *Result, reading, char string) bool {
	t.Helper()
	for _, e := range res.Lexicon.Exact(zhuyin.ParseReading(reading)) {
		if e.Surface == char {
			return e.Rare
		}
	}
	t.Fatalf("%s 打不出 %s", reading, char)
	return false
}

// #171：只來自罕用來源的字，有詞頻也標為罕用。維基、新聞的次數也數到日文新字體（気 249 次），
// 有詞頻就當常用字的話，「空気」這類 bigram 會在單字候選把気排到氣前面。
func TestRareSourceCharsAreRareEvenWithCounts(t *testing.T) {
	d := load(t, fixture)
	d.Chars = append(d.Chars,
		CharReading{Char: "氣", Reading: zhuyin.ParseReading("ㄑㄧˋ")[0], Grade: Primary, Norm: true, Origins: []string{"imp", "curated"}},
		CharReading{Char: "気", Reading: zhuyin.ParseReading("ㄑㄧˋ")[0], Grade: Primary, Norm: true, Origins: []string{"imp"}},
		CharReading{Char: "炁", Reading: zhuyin.ParseReading("ㄑㄧˋ")[0], Grade: Primary, Norm: true, Origins: []string{"imp"}},
	)
	d.Counts["toycorpus"]["気"] = 249
	d.Counts["toycorpus"]["氣"] = 41261
	o := DefaultOptions()
	o.Rare = "imp"
	res := Build(d, o)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	if !rareOf(t, res, "ㄑㄧˋ", "気") {
		t.Error("只來自罕用來源的字有詞頻也要標為罕用")
	}
	if !rareOf(t, res, "ㄑㄧˋ", "炁") {
		t.Error("沒有詞頻的罕用來源字照舊是罕用")
	}
	if rareOf(t, res, "ㄑㄧˋ", "氣") {
		t.Error("另有一般來源的字不是罕用")
	}
	// 罕用只影響候選排序，詞頻照舊進語言模型（整句解碼不變）。
	if res.LM.Uni("気") <= res.LM.Uni("炁") {
		t.Errorf("罕用字的詞頻不應被拿掉：気 %g、炁 %g", res.LM.Uni("気"), res.LM.Uni("炁"))
	}
}

// 整份詞庫：只來自全字庫或小麥注音非 Big5 字集的單字一律是罕用，其他單字都不是；
// 回報裡的日文新字體與簡化字排在常用字後面，而且仍打得出來。
func TestShippedLexiconRanksNonBig5CharsAfterCommonOnes(t *testing.T) {
	if testing.Short() {
		t.Skip("編譯整份詞庫")
	}
	d := load(t, "../../data")
	o := DefaultOptions()
	o.Rare = rareSources
	res := Build(d, o)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors[:min(5, len(res.Errors))])
	}
	rare := map[string]bool{}
	for _, x := range strings.Split(rareSources, ",") {
		rare[x] = true
	}
	common := map[string]bool{} // 至少有一個一般來源的字
	seen := map[string]bool{}
	for _, c := range d.Chars {
		seen[c.Char] = true
		for _, x := range c.Origins {
			if !rare[x] && !strings.HasPrefix(x, "rule:") {
				common[c.Char] = true
			}
		}
	}
	for _, w := range d.Words {
		common[w.Word] = true
	}
	wrong, rareCount := 0, 0
	for ch := range seen {
		for _, e := range res.Lexicon.Lookup(ch) {
			if e.Rare {
				rareCount++
			}
			if e.Rare == common[ch] {
				if wrong++; wrong <= 10 {
					t.Errorf("%s %s：罕用 %v，但一般來源 %v", ch, zhuyin.Key(e.Reading), e.Rare, common[ch])
				}
			}
		}
	}
	if rareCount == 0 {
		t.Fatal("詞庫裡沒有罕用字，rareSources 與資料不符")
	}
	for _, c := range []struct{ reading, variant, standard string }{
		{"ㄑㄧˋ", "気", "氣"}, {"ㄊㄨˊ", "図", "圖"}, {"ㄇㄞˋ", "売", "賣"}, {"ㄍㄨㄤˇ", "広", "廣"},
		{"ㄓㄢˋ", "戦", "戰"}, {"ㄉㄨˊ", "読", "讀"}, {"ㄧˋ", "駅", "驛"}, {"ㄍㄨㄛˊ", "国", "國"},
		{"ㄕㄨㄛ", "説", "說"}, {"ㄨㄟˊ", "爲", "為"}, {"ㄔㄢˇ", "産", "產"},
	} {
		if !rareOf(t, res, c.reading, c.variant) || rareOf(t, res, c.reading, c.standard) {
			t.Errorf("%s：%s 應是罕用、%s 不是", c.reading, c.variant, c.standard)
		}
	}
}
