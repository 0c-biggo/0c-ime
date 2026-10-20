package decode

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// weakFixture 取 dev.63 資料包的實際數值（#107）：ㄈㄚˋ 自己的字「法」，「髮」讀 ㄈㄚˋ 只出現在「髮型」；
// 「突」只在「突然 ㄊㄨ ㄖㄢˊ」讀一聲，比 ㄊㄨ 自己的字「禿」常見得多（前綴證據強）。
func weakFixture() (*lexicon.Mem, bigramScore) {
	lex := lexicon.NewMem()
	b := bigramScore{uni: map[string]float64{}, bi: map[[2]string]float64{}}
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"這個", "ㄓㄜˋ ㄍㄜ˙", 2e-3}, {"法", "ㄈㄚˋ", 1.46e-4}, {"髮型", "ㄈㄚˋ ㄒㄧㄥˊ", 8.52e-6},
		{"跆拳道", "ㄊㄞˊ ㄑㄩㄢˊ ㄉㄠˋ", 4e-6}, {"臺", "ㄊㄞˊ", 1e-4}, {"全", "ㄑㄩㄢˊ", 2e-4},
		{"禿", "ㄊㄨ", 4.29e-6}, {"突然", "ㄊㄨ ㄖㄢˊ", 1.4e-4},
		{"電", "ㄉㄧㄢˋ", 2e-4}, {"店", "ㄉㄧㄢˋ", 1e-4}, {"電腦", "ㄉㄧㄢˋ ㄋㄠˇ", 3e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		b.uni[e.w] = e.p
	}
	return lex, b
}

func TestWeakPrefixMarksSingleSyllableHeadOfLongerWord(t *testing.T) {
	lex, b := weakFixture()
	d := &Decoder{Lex: lex, Score: b, Continue: .5, SeparateContinuation: true}
	for _, c := range []struct {
		reading, top string
		weak         map[string]bool // 列出的文字是否標為 WeakPrefix
	}{
		// 統計首選本來就是完整的「法」；「髮」只是髮型的開頭，單獨讀 ㄈㄚˋ 不對，標記後評分器不能換成它。
		{"ㄓㄜˋ ㄍㄜ˙ ㄈㄚˋ", "這個法", map[string]bool{"這個法": false, "這個髮": true}},
		{"ㄈㄚˋ", "法", map[string]bool{"法": false, "髮": true}},
		// 前綴證據強：統計照樣選「突」（標記讓 session 知道首選本身就是這種前綴，不擋評分器）。
		{"ㄊㄨ", "突", map[string]bool{"突": true, "禿": false}},
		// 打了兩個音節的長詞開頭、或顯示的字本身就是這個讀音的字，都不算。
		{"ㄊㄞˊ ㄑㄩㄢˊ", "跆拳", map[string]bool{"跆拳": false}},
		{"ㄉㄧㄢˋ", "電", map[string]bool{"電": false, "店": false}},
	} {
		rows, _ := d.NBest(enLat(c.reading), nil, true, 8)
		if len(rows) == 0 || rows[0].Text != c.top {
			t.Errorf("%s 首選 %+v，應為 %s", c.reading, rows, c.top)
			continue
		}
		seen := map[string]bool{}
		for _, r := range rows {
			if want, ok := c.weak[r.Text]; ok {
				seen[r.Text] = true
				if r.WeakPrefix != want {
					t.Errorf("%s 的 %s WeakPrefix=%v，應為 %v", c.reading, r.Text, r.WeakPrefix, want)
				}
			}
		}
		for w := range c.weak {
			if !seen[w] {
				t.Errorf("%s 的 N-best 沒有 %s：%+v", c.reading, w, rows)
			}
		}
	}
}
