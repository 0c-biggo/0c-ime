package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/vocab"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #122：自訂詞比照 #96 受保護。評分器偏好常用的「怡君→宜君」寫法、分數遠大於統計差距時，
// 也不能把使用者加的「陳怡君」換掉，不提示換回去；沒有自訂詞時評分器照常改寫（測試前提）。
func TestNeuralKeepsCustomWord(t *testing.T) {
	saved := neuralKeeps
	neuralKeeps = nil
	t.Cleanup(func() { neuralKeeps = saved })
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"陳", "ㄔㄣˊ", 1e-3}, {"怡", "ㄧˊ", 1e-5}, {"宜", "ㄧˊ", 1e-4}, {"君", "ㄐㄩㄣ", 1e-4},
		{"宜君", "ㄧˊ ㄐㄩㄣ", 1e-5}, {"甲乙", "ㄐㄧㄚˇ ㄧˇ", 1e-6}, {"丙丁戊", "ㄅㄧㄥˇ ㄉㄧㄥ ㄨˋ", 1e-6},
		{"今天", "ㄐㄧㄣ ㄊㄧㄢ", 1e-3}, {"今", "ㄐㄧㄣ", 1e-4}, {"天", "ㄊㄧㄢ", 1e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	m := personal.NewModel(lex, sys)
	s := New(m)
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: 2, HintGap: 100, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = 13 * float64(strings.Count(c.Text, "宜"))
		}
		return scores, nil
	})}
	if got := shown(s, "ㄔㄣˊ ㄧˊ ㄐㄩㄣ").Text(); got != "陳宜君" {
		t.Fatalf("測試前提：評分器應把首選換成「陳宜君」，實得 %q", got)
	}
	if _, err := m.VocabAdd([]vocab.Entry{{Text: "陳怡君", Readings: []string{"ㄔㄣˊ ㄧˊ ㄐㄩㄣ"}}}, vocab.SourceManual); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ reading, want string }{
		{"ㄔㄣˊ ㄧˊ ㄐㄩㄣ", "陳怡君"},
		{"ㄐㄧㄣ ㄊㄧㄢ ㄔㄣˊ ㄧˊ ㄐㄩㄣ", "今天陳怡君"},
	} {
		snap := shown(s, c.reading)
		if snap.Text() != c.want {
			t.Errorf("%s 顯示 %q，want %q", c.reading, snap.Text(), c.want)
		}
		if strings.Contains(snap.Suggestion, "宜") {
			t.Errorf("%s 不該提示換回「宜」：%q", c.reading, snap.Suggestion)
		}
	}
}
