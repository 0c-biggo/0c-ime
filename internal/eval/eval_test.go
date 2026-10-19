package eval

import (
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/zhuyin"
	"testing"
	"time"
)

func TestChunks(t *testing.T) {
	got := Chunks("我想吃麻婆豆腐，但是API壞了。好", 2)
	if len(got) != 3 || got[0] != "我想吃麻婆豆腐" || got[1] != "但是" || got[2] != "壞了" {
		t.Errorf("%q", got)
	}
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		d    int
	}{{"親子丼", "親子景", 1}, {"", "abc", 3}, {"麻婆豆腐", "嘛婆鬥", 3}, {"一樣", "一樣", 0}} {
		if got := EditDistance(c.a, c.b); got != c.d {
			t.Errorf("%q %q：%d 應為 %d", c.a, c.b, got, c.d)
		}
	}
}

func TestPercentile(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i))
	}
	if Percentile(ds, 50) != 50 || Percentile(ds, 99) != 99 || Percentile(ds, 100) != 100 {
		t.Error(Percentile(ds, 50), Percentile(ds, 99))
	}
}

// 讀音同分時的決勝：先看各字的單字讀音等級，再看這個字在詞裡最常讀哪個音；不看詞庫順序。
func TestAnnotateBreaksTies(t *testing.T) {
	sys := lm.NewMem()
	var entries []lexicon.Entry
	add := func(w, r string, prw, p float64) {
		entries = append(entries, lexicon.Entry{Surface: w, Reading: zhuyin.ParseReading(r), PRW: prw})
		sys.SetUni(w, p)
	}
	add("麗", "ㄌㄧˊ", 0.1, 1e-4) // 來源沒有給主讀音：兩個讀音同分，ㄌㄧˊ 在詞庫順序排前面
	add("麗", "ㄌㄧˋ", 0.1, 1e-4)
	add("美麗", "ㄇㄟˇ ㄌㄧˋ", 1, 1e-3)
	add("高麗", "ㄍㄠ ㄌㄧˊ", 1, 1e-5)
	add("埔", "ㄅㄨˇ", 0.5, 1e-5)
	add("埔", "ㄆㄨˇ", 1, 1e-5)
	add("大埔", "ㄉㄚˋ ㄅㄨˇ", 1, 1e-4) // 來源給了兩個同等級的讀音
	add("大埔", "ㄉㄚˋ ㄆㄨˇ", 1, 1e-4)
	add("大", "ㄉㄚˋ", 1, 1e-3)
	lex, err := lexicon.Build(entries)
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(lex, sys)
	for text, want := range map[string]string{"麗": "ㄌㄧˋ", "大埔": "ㄉㄚˋ ㄆㄨˇ"} {
		got, ok := r.Annotate(text)
		if !ok || zhuyin.Key(got) != zhuyin.Key(zhuyin.ParseReading(want)) {
			t.Errorf("%s 應標為 %s，得到 %v", text, want, got)
		}
	}
}
