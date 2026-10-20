package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// enSession 取 dev.62 資料包的實際數值（#139）：「恩」頻率與 恩→恩 bigram 高，「嗯嗯」只有補足詞頻；
// 評分器照語料寫法偏好「恩恩」。
func enSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"恩", "ㄣ", 5.1e-5}, {"嗯", "ㄣ", 5e-5}, {"嗯嗯", "ㄣ ㄣ", 5.3e-8},
		{"怨", "ㄩㄢˋ", 3e-5}, {"恩恩怨怨", "ㄣ ㄣ ㄩㄢˋ ㄩㄢˋ", 6.9e-7},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi(lm.BOS, "恩", 3e-5)
	sys.SetBi("恩", "恩", 5.9e-3)
	s := New(personal.NewModel(lex, sys))
	s.Rerank = rerank.Options{Weight: 2, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = -12
			switch {
			case strings.HasPrefix(c.Text, "恩恩"):
				scores[i] = -.05
			case c.Text == "嗯嗯":
				scores[i] = -3
			}
		}
		return scores, nil
	})}
	return s
}

func TestNeuralDoesNotTurnLexiconRepeatBackIntoNonWord(t *testing.T) {
	s := enSession()
	if got := typeSyl(s, "ㄣ").Text(); got != "恩" {
		t.Fatalf("ㄣ → %q", got)
	}
	if got := typeSyl(s, "ㄣ").Text(); got != "嗯嗯" {
		t.Fatalf("ㄣ ㄣ → %q，應為 嗯嗯", got)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "嗯嗯" {
		t.Fatalf("提交 %q", got)
	}
	// 打完整個 AABB 成語時不受影響。
	for _, y := range []string{"ㄣ", "ㄣ", "ㄩㄢˋ", "ㄩㄢˋ"} {
		typeSyl(s, y)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "恩恩怨怨" {
		t.Fatalf("恩恩怨怨 → %q", got)
	}
}

// 0.2.0 詞庫（850a075）的數值：嗯嗯本身的詞頻夠高，統計引擎不必壓分就選嗯嗯；評分器照語料寫法偏好
// 恩恩時，疊字仍不能換回首選（#139 在新詞庫的退步）。
func TestNeuralDoesNotTurnFrequentLexiconRepeatBackIntoNonWord(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{{"恩", "ㄣ", 8.66e-5}, {"嗯", "ㄣ", 3.98e-5}, {"嗯嗯", "ㄣ ㄣ", 1.72e-6}} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi("恩", "恩", 5.13e-3)
	s := New(personal.NewModel(lex, sys))
	s.Rerank = rerank.Options{Weight: 2, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = -12
			switch c.Text {
			case "恩恩":
				scores[i] = -.05
			case "嗯嗯":
				scores[i] = -3
			}
		}
		return scores, nil
	})}
	typeSyl(s, "ㄣ")
	if got := typeSyl(s, "ㄣ").Text(); got != "嗯嗯" {
		t.Fatalf("ㄣ ㄣ → %q，應為 嗯嗯", got)
	}
}

// 使用者自己選過、提交過「恩恩」之後，照個人用法顯示，評分器也可以選它。
func TestRepeatVetoYieldsToPersonalUse(t *testing.T) {
	s := enSession()
	s.model.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄣ ㄣ"), Path: []personal.Seg{
		{Word: "恩", Start: 0, End: 1, Manual: true}, {Word: "恩", Start: 1, End: 2, Manual: true},
	}})
	typeSyl(s, "ㄣ")
	if got := typeSyl(s, "ㄣ").Text(); got != "恩恩" {
		t.Fatalf("個人用過的疊字：%q", got)
	}
}
