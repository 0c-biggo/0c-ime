package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/decode"
	"git.biggo.com/0c/ime/core/rerank"
)

func hyp(segs ...decode.Seg) decode.Hypothesis {
	var b strings.Builder
	for _, g := range segs {
		b.WriteString(g.Word)
	}
	return decode.Hypothesis{Text: b.String(), Segments: segs}
}

func vetoedTexts(rows []decode.Hypothesis, vetoed []bool) []string {
	var out []string
	for i, v := range vetoed {
		if v {
			out = append(out, rows[i].Text)
		}
	}
	return out
}

// #99：候選文字字數與音節數不同（尾端補全顯示整個詞、原樣片段）時，「不推翻使用者選擇」與
// neuralKeeps 原本整段不檢查或直接略過該列。改依分段把字對回音節：對得上的區間照常比對，
// 對不上又與受保護區間重疊的候選一律排除。
func TestNeuralKeepsHoldWhenCandidateLengthDiffersFromReading(t *testing.T) {
	s := newShemeSessionWithKeeps(t)
	shown(s, "ㄕㄜˊ ㄇㄜ˙ ㄕˊ")
	s.configureCorrections(0, 3, true)
	rows := []decode.Hypothesis{
		hyp(decode.Seg{Word: "什麼", Start: 0, End: 2}, decode.Seg{Word: "時候", Start: 2, End: 3, Partial: true}),
		hyp(decode.Seg{Word: "甚麼", Start: 0, End: 2}, decode.Seg{Word: "時候", Start: 2, End: 3, Partial: true}),
		hyp(decode.Seg{Word: "甚麼", Start: 0, End: 2}, decode.Seg{Word: "時", Start: 2, End: 3, Partial: true}),
		hyp(decode.Seg{Word: "甚麼時候", Start: 0, End: 3}), // 對不齊且蓋住「什麼」：保守排除
		hyp(decode.Seg{Word: "什麼", Start: 0, End: 2}, decode.Seg{Word: "時", Start: 2, End: 3}),
		hyp(decode.Seg{Word: "什麼", Start: 0, End: 2}, decode.Seg{Word: "時候啊", Start: 2, End: 3}), // 對不齊處不在保護區間
	}
	got := vetoedTexts(rows, s.overturnsUser(rows, 0, 3))
	want := []string{"甚麼時候", "甚麼時", "甚麼時候"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("排除 %q，want %q", got, want)
	}
}

func TestNeuralKeepsUserChoiceWhenCandidateLengthDiffersFromReading(t *testing.T) {
	s := newShemeSession(t)
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	selectText(t, s, 0, "什麼")
	commitText(s)
	shown(s, "ㄕㄜˊ ㄇㄜ˙ ㄕˊ")
	s.configureCorrections(0, 3, true)
	rows := []decode.Hypothesis{
		hyp(decode.Seg{Word: "什麼", Start: 0, End: 2}, decode.Seg{Word: "時", Start: 2, End: 3, Partial: true}),
		hyp(decode.Seg{Word: "甚麼", Start: 0, End: 2}, decode.Seg{Word: "時候", Start: 2, End: 3, Partial: true}),
		hyp(decode.Seg{Word: "甚麼時候", Start: 0, End: 3}),
		hyp(decode.Seg{Word: "什麼", Start: 0, End: 2}, decode.Seg{Word: "時候", Start: 2, End: 3, Partial: true}),
	}
	vetoed := s.overturnsUser(rows, 0, 3)
	if got := vetoedTexts(rows, vetoed); strings.Join(got, ",") != "甚麼時候,甚麼時候" {
		t.Fatalf("排除 %q，want 兩列「甚麼時候」", got)
	}
	// 評分器偏好「甚」：排除後不能換上，也不能當成提示。
	scores := []float64{0, 13, 13, 0}
	d := decide(rows, scores, 2, 0, vetoed)
	if d.winner != 0 && d.winner != 3 || d.second == 1 || d.second == 2 {
		t.Fatalf("決策 %+v 換上或提示了推翻使用者選擇的候選", d)
	}
}

// 首選本身對不上音節時，有語境更正的位置仍受保護：寫法相同的分段不算改寫，其他對不上的寫法排除。
func TestNeuralKeepsCorrectionUnderUnalignedSegment(t *testing.T) {
	s := newShemeSession(t)
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	selectText(t, s, 0, "什麼")
	commitText(s)
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	s.configureCorrections(0, 2, true)
	if s.dec.CorrectionPenalty == nil {
		t.Fatal("測試前提：同語境應有更正")
	}
	rows := []decode.Hypothesis{
		hyp(decode.Seg{Word: "什麼？", Start: 0, End: 2}),
		{Text: "什麼？", Segments: []decode.Seg{{Word: "什麼？", Start: 0, End: 2}}},
		hyp(decode.Seg{Word: "甚麼？", Start: 0, End: 2}),
		hyp(decode.Seg{Word: "什麼", Start: 0, End: 2}),
		hyp(decode.Seg{Word: "甚麼", Start: 0, End: 2}),
	}
	got := vetoedTexts(rows, s.overturnsUser(rows, 0, 2))
	if strings.Join(got, ",") != "甚麼？,甚麼" {
		t.Fatalf("排除 %q，want 「甚麼？」「甚麼」", got)
	}
}

// 實際的神經評分路徑：允許補全候選時，段內帶尾端補全也照使用者選的「什麼」，評分器確實收到補全候選。
func TestNeuralKeepsUserChoiceWithPartialTail(t *testing.T) {
	s := newShemeSession(t)
	inner := s.Rerank.Scorer
	partial := false
	s.Rerank.AllowPartial = true
	s.Rerank.Scorer = rankFunc(func(r rerank.Request) ([]float64, error) {
		for _, c := range r.Candidates {
			for _, g := range c.Segments {
				partial = partial || g.Partial
			}
		}
		return inner.Score(r)
	})
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	selectText(t, s, 0, "什麼")
	commitText(s)
	s.Opt.PrefixAware = true
	if got := shown(s, "ㄕㄜˊ ㄇㄜ˙ ㄕˊ").Text(); got != "什麼時" {
		t.Fatalf("顯示 %q，want 「什麼時」", got)
	}
	if !partial {
		t.Fatal("測試前提：評分器應收到帶尾端補全的候選")
	}
}
