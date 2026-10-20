package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 系統語言模型在「ㄊㄚ ㄉㄜ˙ ㄋㄢˊ ㄆㄥˊ ㄧㄡˇ」偏好「她」：前後文推得出女性，但不能因此自動切換。
func newPronounSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"他", "ㄊㄚ", 1e-2}, {"她", "ㄊㄚ", 4e-3}, {"的", "ㄉㄜ˙", 3e-2},
		{"他們", "ㄊㄚ ㄇㄣ˙", 2e-3}, {"她們", "ㄊㄚ ㄇㄣ˙", 5e-4},
		{"男朋友", "ㄋㄢˊ ㄆㄥˊ ㄧㄡˇ", 1e-4}, {"女朋友", "ㄋㄩˇ ㄆㄥˊ ㄧㄡˇ", 1e-4},
		{"吉他", "ㄐㄧˊ ㄊㄚ", 1e-4}, {"很", "ㄏㄣˇ", 5e-3}, {"好", "ㄏㄠˇ", 5e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi("她", "的", 0.5)
	sys.SetBi("的", "男朋友", 0.2)
	sys.SetBi(lm.BOS, "她們", 0.5)
	return New(personal.NewModel(lex, sys))
}

func typeAll(s *Session, reading string) proto.Snapshot {
	var snap proto.Snapshot
	for _, y := range strings.Fields(reading) {
		snap = typeSyl(s, y)
	}
	return snap
}

// pick 在音節位置 pos 開一字候選並選 word。
func pick(t *testing.T, s *Session, pos int, word string) {
	t.Helper()
	s.Handle(proto.Request{Type: proto.Cursor, To: pos})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	for _, it := range snap.Candidates.Items {
		if it.Text == word {
			s.Handle(proto.Request{Type: proto.Select, Index: it.ID})
			return
		}
	}
	t.Fatalf("候選沒有 %q：%+v", word, snap.Candidates.Items)
}

func commitText(s *Session) string {
	return s.Handle(proto.Request{Type: proto.Commit}).Commit
}

const boyfriend = "ㄊㄚ ㄉㄜ˙ ㄋㄢˊ ㄆㄥˊ ㄧㄡˇ"
const girlfriend = "ㄊㄚ ㄉㄜ˙ ㄋㄩˇ ㄆㄥˊ ㄧㄡˇ"

func TestPronounDefaultsToNeutralDespiteContext(t *testing.T) {
	s := newPronounSession()
	snap := typeAll(s, boyfriend)
	if raw := s.decodeWithLiterals(); raw[0].Word != "她" {
		t.Fatalf("測試前提：系統語境應偏好「她」，實得 %+v", raw)
	}
	if snap.Text() != "他的男朋友" {
		t.Fatalf("沒有使用者習慣時應預設中性字：%q", snap.Text())
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap = s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if snap.Candidates.Items[0].Text != "他" || snap.Candidates.Items[1].Text != "她" {
		t.Fatalf("候選應中性字在前、變體緊接在後：%+v", snap.Candidates.Items)
	}
	s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEscape})
	if got := commitText(s); got != "他的男朋友" {
		t.Fatalf("提交：%q", got)
	}
	s = newPronounSession()
	if snap = typeAll(s, "ㄊㄚ ㄇㄣ˙"); snap.Text() != "他們" {
		t.Fatalf("多字詞也預設中性：%q", snap.Text())
	}
}

// 使用者在同一個詞旁反覆把「他」訂正為「她」才成為預設；只對有這個詞的語境生效。
func TestPronounHabitFollowsRepeatedCorrections(t *testing.T) {
	s := newPronounSession()
	for round := 1; round <= 2; round++ {
		if snap := typeAll(s, boyfriend); snap.Text() != "他的男朋友" {
			t.Fatalf("第 %d 次訂正前仍應是中性字：%q", round, snap.Text())
		}
		pick(t, s, 0, "她")
		if got := commitText(s); got != "她的男朋友" {
			t.Fatalf("手選後提交：%q", got)
		}
	}
	if snap := typeAll(s, boyfriend); snap.Text() != "她的男朋友" {
		t.Fatalf("訂正兩次後應以使用者習慣為預設：%q", snap.Text())
	}
	commitText(s)
	if snap := typeAll(s, girlfriend); snap.Text() != "他的女朋友" {
		t.Fatalf("習慣只在「男朋友」旁生效，不能擴及「ㄊㄚ ㄉㄜ˙」：%q", snap.Text())
	}
	commitText(s)

	// 在同一個詞旁改回中性字一次，就不再自動用變體。
	typeAll(s, boyfriend)
	pick(t, s, 0, "他")
	commitText(s)
	if snap := typeAll(s, boyfriend); snap.Text() != "他的男朋友" {
		t.Fatalf("改回中性字後應停止自動用變體：%q", snap.Text())
	}
}

func TestPronounForgetClearsHabit(t *testing.T) {
	s := newPronounSession()
	for range 2 {
		typeAll(s, boyfriend)
		pick(t, s, 0, "她")
		commitText(s)
	}
	s.Handle(proto.Request{Type: proto.Forget, Word: "她"})
	if snap := typeAll(s, boyfriend); snap.Text() != "他的男朋友" {
		t.Fatalf("撤銷「她」後應回到中性字：%q", snap.Text())
	}
}

// 神經選字因語境改選「她」時同樣改回中性字，也不以 Tab 提示建議改成「她」。
func TestPronounNeuralNeitherSwitchesNorSuggests(t *testing.T) {
	s := newPronounSession()
	sawVariant := false
	s.Rerank = rerank.Options{Weight: 1, HintGap: 100, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			if strings.ContainsRune(c.Text, '她') {
				scores[i], sawVariant = 50, true
			}
		}
		return scores, nil
	})}
	snap := typeAll(s, "ㄊㄚ ㄏㄣˇ ㄏㄠˇ")
	if !sawVariant {
		t.Fatal("測試前提：評分器應收到含「她」的候選")
	}
	if snap.Text() != "他很好" || strings.Contains(snap.Suggestion, "她") {
		t.Fatalf("神經選字不得切換或建議性別：%q 提示 %q", snap.Text(), snap.Suggestion)
	}
	if got := commitText(s); got != "他很好" {
		t.Fatalf("提交：%q", got)
	}
}

// 以 Tab 接受神經提示後提交，提示中的代名詞不算明確選取。
func TestPronounAcceptedHintIsNotExplicit(t *testing.T) {
	s := newPronounSession()
	typeAll(s, "ㄊㄚ ㄏㄣˇ ㄏㄠˇ")
	s.hint = &neuralHint{text: "他很好", replace: s.snapshot().Text(), segs: []decode.Seg{{Word: "他", Start: 0, End: 1}, {Word: "很", Start: 1, End: 2}, {Word: "好", Start: 2, End: 3}}}
	if !s.acceptHint() {
		t.Fatal("接受提示失敗")
	}
	if got := commitText(s); got != "他很好" {
		t.Fatalf("提交：%q", got)
	}
	if all := s.model.Store.Pronouns["他"].All; all.ManualNeutral != 0 || all.Neutral == 0 {
		t.Fatalf("接受提示應只算被動用法：%+v", all)
	}
}
