package suggest

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

type score map[string]float64

func (s score) Prob(_, w string) float64 { return s[w] }

// 同樣信心，省一鍵與省多鍵的建議應有不同效益；不用真實時間單位假裝量過使用者速度。
func TestBenefitDependsOnRemainingTyping(t *testing.T) {
	for _, tc := range []struct {
		word, reading string
		want          bool
	}{
		{"麻吃", "ㄇㄚˊ ㄔ", false},
		{"麻婆豆腐", "ㄇㄚˊ ㄆㄛˊ ㄉㄡˋ ㄈㄨˇ", true},
	} {
		lex := lexicon.NewMem()
		lex.Add(lexicon.Entry{Surface: "麻", Reading: zhuyin.ParseReading("ㄇㄚˊ"), PRW: 1})
		lex.Add(lexicon.Entry{Surface: tc.word, Reading: zhuyin.ParseReading(tc.reading), PRW: 1})
		d := &decode.Decoder{Lex: lex, Score: score{"麻": 0.5, tc.word: 0.5}}
		lat := input.Exact{}.Expand(zhuyin.ParseReading("ㄇㄚˊ"))
		path := []decode.Seg{{Word: "麻", End: 1, Partial: true}}
		// 讓較長詞略勝，測試效益門檻而非候選排序同分。
		d.Score = score{"麻": 0.49, tc.word: 0.51}
		o := DefaultOptions()
		o.AttentionCost = 0.6
		if got := For(d, lat, path, o); (got != nil) != tc.want {
			t.Fatalf("%s：有無建議=%v，預期=%v", tc.word, got != nil, tc.want)
		}
		o.AttentionCost = 0
		if For(d, lat, path, o) == nil {
			t.Fatal("關閉效益門檻後原候選應可顯示")
		}
	}
}
