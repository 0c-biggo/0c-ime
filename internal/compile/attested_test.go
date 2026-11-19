package compile

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #320：高頻字的可考讀音（日 ㄇㄧˋ，只用在「金日磾」）不得只因字頻高就排到該音的常用字（密）前面；
// 常用詞裡真的唸這個音的（參與 ㄘㄢ ㄩˋ）照比例保留；證據少的罕用字維持等級值；多字詞的讀音不受影響；
// 同音只有罕用字或其他可考讀音時不壓低（ㄓㄟˋ 只有「這」與「这」，壓低會讓「这个」蓋過「這個」）；
// 讀音全是可考的字（哦）維持原狀。
func TestAttestedReadingFollowsHowOftenTheCharIsReadThatWay(t *testing.T) {
	d := load(t, fixture)
	ch := func(c, r string, g Grade) CharReading {
		return CharReading{Char: c, Reading: zhuyin.ParseReading(r)[0], Grade: g, Norm: true, Origins: []string{"curated"}}
	}
	word := func(w, r string) WordReading {
		return WordReading{Word: w, Reading: zhuyin.ParseReading(r), Grade: Primary, Norm: true, Origins: []string{"curated"}}
	}
	d.Chars = append(d.Chars,
		ch("日", "ㄖˋ", Primary), ch("日", "ㄇㄧˋ", Attested), ch("密", "ㄇㄧˋ", Primary),
		ch("金", "ㄐㄧㄣ", Primary), ch("磾", "ㄉㄧ", Primary),
		ch("與", "ㄩˇ", Primary), ch("與", "ㄩˋ", Attested), ch("玉", "ㄩˋ", Primary), ch("參", "ㄘㄢ", Primary),
		ch("乂", "ㄧˋ", Primary), ch("乂", "ㄞˋ", Attested), ch("愛", "ㄞˋ", Primary),
		ch("這", "ㄓㄜˋ", Primary), ch("這", "ㄓㄟˋ", Attested), ch("这", "ㄓㄟˋ", Attested),
		ch("黑", "ㄏㄟ", Primary), ch("黑", "ㄏㄟˇ", Attested),
		ch("哦", "ㄛˊ", Attested), ch("哦", "ㄜˊ", Attested), ch("鵝", "ㄜˊ", Primary),
		CharReading{Char: "黒", Reading: zhuyin.ParseReading("ㄏㄟˇ")[0], Grade: Primary, Norm: true, Origins: []string{"imp"}},
	)
	d.Words = append(d.Words, word("金日磾", "ㄐㄧㄣ ㄇㄧˋ ㄉㄧ"), word("參與", "ㄘㄢ ㄩˋ"), word("日本", "ㄖˋ ㄅㄣˇ"))
	c := d.Counts["toycorpus"]
	for w, n := range map[string]float64{"日": 100000, "密": 1000, "日本": 30000, "金日磾": 1,
		"與": 50000, "玉": 1000, "參與": 20000, "愛": 5000, "這": 80000, "这": 10, "黑": 20000, "黒": 10, "哦": 30000, "鵝": 3000} {
		c[w] = n
	}
	o := DefaultOptions()
	o.Rare = "imp"
	res := Build(d, o)
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	score := func(r, w string) float64 { return res.LM.Uni(w) * prw(res.Lexicon, r, w) }
	if score("ㄇㄧˋ", "密") <= score("ㄇㄧˋ", "日") {
		t.Errorf("ㄇㄧˋ：日 %g 不該排在 密 %g 前面", score("ㄇㄧˋ", "日"), score("ㄇㄧˋ", "密"))
	}
	if prw(res.Lexicon, "ㄇㄧˋ", "日") <= 0 {
		t.Error("罕用讀音仍要打得到")
	}
	if !near(prw(res.Lexicon, "ㄐㄧㄣ ㄇㄧˋ ㄉㄧ", "金日磾"), 1) {
		t.Errorf("多字詞的讀音不受影響：金日磾 %g", prw(res.Lexicon, "ㄐㄧㄣ ㄇㄧˋ ㄉㄧ", "金日磾"))
	}
	if !near(prw(res.Lexicon, "ㄩˋ", "與"), Attested.PRW()) {
		t.Errorf("常用詞裡唸這個音（參與）就保留等級值：與 ㄩˋ %g", prw(res.Lexicon, "ㄩˋ", "與"))
	}
	if !near(prw(res.Lexicon, "ㄞˋ", "乂"), Attested.PRW()) {
		t.Errorf("沒有詞頻的罕用字維持等級值：乂 ㄞˋ %g", prw(res.Lexicon, "ㄞˋ", "乂"))
	}
	for _, x := range [][2]string{{"ㄓㄟˋ", "這"}, {"ㄏㄟˇ", "黑"}} {
		if !near(prw(res.Lexicon, x[0], x[1]), Attested.PRW()) {
			t.Errorf("同音沒有常用字就不壓低：%s %s %g", x[1], x[0], prw(res.Lexicon, x[0], x[1]))
		}
	}
	if !near(prw(res.Lexicon, "ㄜˊ", "哦"), Attested.PRW()) {
		t.Errorf("讀音全是可考的字維持原狀：哦 ㄜˊ %g", prw(res.Lexicon, "ㄜˊ", "哦"))
	}
	if !near(prw(res.Lexicon, "ㄖˋ", "日"), 1) {
		t.Errorf("主讀音不受影響：日 ㄖˋ %g", prw(res.Lexicon, "ㄖˋ", "日"))
	}

	o.AttestedMass = 0
	old := Build(d, o)
	if !near(prw(old.Lexicon, "ㄇㄧˋ", "日"), Attested.PRW()) {
		t.Errorf("-attested-mass 0 是舊公式：日 ㄇㄧˋ %g", prw(old.Lexicon, "ㄇㄧˋ", "日"))
	}
}
