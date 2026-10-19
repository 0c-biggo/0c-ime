package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/rerank"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// weakPrefixSession 取 dev.63 資料包的實際數值（#107 同型）：ㄈㄚˋ 有自己的字「法」，「髮」讀 ㄈㄚˋ 只在
// 「髮型」裡；「突」只在「突然」讀 ㄊㄨ，比 ㄊㄨ 自己的字「禿」常見得多。評分器只看文字，偏好 favor 指定的字
// 用來驗證詞內字的高分不能覆蓋精確可達的獨立字。
func weakPrefixSession(favor string) *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"這個", "ㄓㄜˋ ㄍㄜ˙", 2e-3}, {"法", "ㄈㄚˋ", 1.46e-4}, {"發", "ㄈㄚˋ", 2e-5}, {"髮型", "ㄈㄚˋ ㄒㄧㄥˊ", 8.52e-6},
		{"型", "ㄒㄧㄥˊ", 5e-5}, {"好", "ㄏㄠˇ", 1e-3}, {"禿", "ㄊㄨ", 4.29e-6}, {"突然", "ㄊㄨ ㄖㄢˊ", 1.4e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Rerank = rerank.Options{Weight: 2, AllowPartial: true, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = -15
			if strings.Contains(c.Text, favor) {
				scores[i] = -1
			}
		}
		return scores, nil
	})}
	return s
}

// 句尾單獨的 ㄈㄚˋ：評分器不能把讀音正確的「法」換成只是「髮型」開頭的「髮」；打完整個詞時照常成詞。
func TestNeuralKeepsCompleteCharOverWeakPrefix(t *testing.T) {
	s := weakPrefixSession("髮")
	typeSyl(s, "ㄓㄜˋ")
	typeSyl(s, "ㄍㄜ˙")
	if got := typeSyl(s, "ㄈㄚˋ").Text(); got != "這個法" {
		t.Fatalf("這個 ㄈㄚˋ → %q，應為 這個法", got)
	}
	if got := typeSyl(s, "ㄒㄧㄥˊ").Text(); got != "這個髮型" {
		t.Fatalf("接著打 ㄒㄧㄥˊ → %q，應為 這個髮型", got)
	}
}

// 前綴證據夠強、統計首選本身就是這種前綴時（突然的「突」），不受限制。
func TestStrongPrefixStillShown(t *testing.T) {
	s := weakPrefixSession("突")
	typeSyl(s, "ㄏㄠˇ")
	if got := typeSyl(s, "ㄊㄨ").Text(); got != "好突" {
		t.Fatalf("好 ㄊㄨ → %q，應為 好突", got)
	}
}
