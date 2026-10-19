package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/rerank"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// newShemeSession 重現 #96：統計偏好「什麼」，評分器卻認定「ㄕㄜˊ」是「甚」，
// 每個「甚」加分乘上權重遠大於有界的個人更正成本（實際套件約 26 對 8）。
// 這組測試驗證「不推翻使用者選擇」本身，不靠 neuralKeeps 的過渡對照表，先暫時清空。
func newShemeSession(t *testing.T) *Session {
	t.Helper()
	saved := neuralKeeps
	neuralKeeps = nil
	t.Cleanup(func() { neuralKeeps = saved })
	return newShemeSessionWithKeeps(t)
}

func newShemeSessionWithKeeps(t testing.TB) *Session {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"什", "ㄕㄜˊ", 1e-5}, {"甚", "ㄕㄜˊ", 1e-4}, {"麼", "ㄇㄜ˙", 1e-5},
		{"什麼", "ㄕㄜˊ ㄇㄜ˙", 1e-3}, {"甚麼", "ㄕㄜˊ ㄇㄜ˙", 5e-5},
		{"蛇", "ㄕㄜˊ", 1e-5}, {"蛇麼", "ㄕㄜˊ ㄇㄜ˙", 1e-8},
		{"為", "ㄨㄟˋ", 1e-3}, {"為什麼", "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙", 2e-4}, {"為甚麼", "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙", 1e-5},
		{"時候", "ㄕˊ ㄏㄡˋ", 1e-3}, {"時", "ㄕˊ", 1e-4}, {"候", "ㄏㄡˋ", 1e-5}, {"是", "ㄕˋ", 1e-2},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Rerank = rerank.Options{Weight: 2, HintGap: 100, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		scores := make([]float64, len(r.Candidates))
		for i, c := range r.Candidates {
			scores[i] = 13*float64(strings.Count(c.Text, "甚")) + 12*float64(strings.Count(c.Text, "蛇"))
		}
		return scores, nil
	})}
	return s
}

func shown(s *Session, reading string) proto.Snapshot {
	s.Handle(proto.Request{Type: proto.Cancel})
	return typeReadingLocal(s, reading)
}

// #96：選過「什麼」之後，神經評分不能再把它換回「甚麼」，也不再提示換回去；
// 同一個詞在其他前後文也照使用者的寫法。
func TestNeuralKeepsWordUserChoseForReading(t *testing.T) {
	s := newShemeSession(t)
	if got := shown(s, "ㄕㄜˊ ㄇㄜ˙").Text(); got != "甚麼" {
		t.Fatalf("測試前提：評分器應把首選換成「甚麼」，實得 %q", got)
	}
	selectText(t, s, 0, "什麼")
	if got := commitText(s); got != "什麼" {
		t.Fatalf("commit %q", got)
	}
	for _, c := range []struct{ reading, want string }{
		{"ㄕㄜˊ ㄇㄜ˙", "什麼"},
		{"ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙", "為什麼"},
		{"ㄕㄜˊ ㄇㄜ˙ ㄕˊ ㄏㄡˋ", "什麼時候"},
		{"ㄕˋ ㄕㄜˊ ㄇㄜ˙", "是什麼"},
	} {
		snap := shown(s, c.reading)
		if snap.Text() != c.want {
			t.Errorf("%s 顯示 %q，want %q", c.reading, snap.Text(), c.want)
		}
		if strings.Contains(snap.Suggestion, "甚") {
			t.Errorf("%s 不應提示換回使用者改掉的寫法：%q", c.reading, snap.Suggestion)
		}
	}
}

// 在「為甚麼」裡選「什麼」，統計路徑會把它包進「為什麼」一個詞；手選仍要被學到（#96）。
// 只選「什」也一樣。
func TestSelectionInsideLongerWordIsLearned(t *testing.T) {
	for _, c := range []struct {
		name, reading string
		at            int
		word, want    string
	}{
		{"word-inside-phrase", "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙", 1, "什麼", "為什麼"},
		{"char-inside-word", "ㄕㄜˊ ㄇㄜ˙", 0, "什", "什麼"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newShemeSession(t)
			shown(s, c.reading)
			selectText(t, s, c.at, c.word)
			s.Handle(proto.Request{Type: proto.Cursor, To: 1 << 30})
			if got := commitText(s); got != c.want {
				t.Fatalf("commit %q want %q", got, c.want)
			}
			if len(s.model.Store.Corrections) != 1 {
				t.Fatalf("被包住的手選沒有留下更正：%+v", s.model.Store.Corrections)
			}
			if got := shown(s, c.reading).Text(); got != c.want {
				t.Fatalf("同語境再打顯示 %q，want %q", got, c.want)
			}
		})
	}
	// 整詞手選記下個人讀音：換個前後文也照使用者的寫法。
	s := newShemeSession(t)
	shown(s, "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙")
	selectText(t, s, 1, "什麼")
	commitText(s)
	if got := shown(s, "ㄕㄜˊ ㄇㄜ˙ ㄕˊ ㄏㄡˋ").Text(); got != "什麼時候" {
		t.Fatalf("其他語境顯示 %q", got)
	}
}

// 兩種寫法都親手選過，就不是固定的寫法偏好：沒有更正的語境交回評分器。
func TestNeuralFreeWhenUserChoseBothSpellings(t *testing.T) {
	s := newShemeSession(t)
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	selectText(t, s, 0, "什麼")
	commitText(s)
	shown(s, "ㄕㄜˊ ㄇㄜ˙ ㄕˊ ㄏㄡˋ")
	selectText(t, s, 0, "甚麼")
	commitText(s)
	if got := shown(s, "ㄕˋ ㄕㄜˊ ㄇㄜ˙").Text(); got != "是甚麼" {
		t.Fatalf("兩種都選過的新語境顯示 %q，want 評分器的「是甚麼」", got)
	}
	if got := shown(s, "ㄕㄜˊ ㄇㄜ˙").Text(); got != "什麼" {
		t.Fatalf("有更正的語境顯示 %q，want 「什麼」", got)
	}
}

// 單字的同音字隨語境而定：有語境更正的位置不被評分器換回，其他語境照常由評分器決定。
func TestNeuralKeepsScopedSingleCharacterCorrection(t *testing.T) {
	calls := 0
	s := neuralSession(&calls)
	typeSyl(s, "ㄨㄛˇ")
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "我井" {
		t.Fatalf("測試前提：評分器偏好「井」，實得 %q", got)
	}
	pick(t, s, 1, "景")
	if got := commitText(s); got != "我景" {
		t.Fatalf("commit %q", got)
	}
	typeSyl(s, "ㄨㄛˇ")
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "我景" {
		t.Fatalf("同語境再打顯示 %q，want 「我景」", got)
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	typeSyl(s, "ㄒㄧㄤˇ")
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "想井" {
		t.Fatalf("其他語境顯示 %q，單字更正不該外推", got)
	}
}

// #96 過渡：沒有個人資料時，神經也不得把「什麼」改寫成「甚麼」；單字與其他詞不受影響，
// 使用者選過「甚麼」之後交回既有規則。
func TestNeuralKeepsCommonSpellingWithoutHistory(t *testing.T) {
	s := newShemeSessionWithKeeps(t)
	for _, c := range []struct{ reading, want string }{
		{"ㄕㄜˊ ㄇㄜ˙", "什麼"},
		{"ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙", "為什麼"},
		{"ㄕˋ ㄕㄜˊ ㄇㄜ˙", "是什麼"},
	} {
		if got := shown(s, c.reading).Text(); got != c.want {
			t.Fatalf("%s 顯示 %q，want %q", c.reading, got, c.want)
		}
	}
	if got := shown(s, "ㄕㄜˊ").Text(); got != "甚" {
		t.Fatalf("單字不在對照表內，評分器照常決定：%q", got)
	}
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	selectText(t, s, 0, "甚麼")
	if got := commitText(s); got != "甚麼" {
		t.Fatalf("commit %q", got)
	}
	if got := shown(s, "ㄕㄜˊ ㄇㄜ˙").Text(); got != "甚麼" {
		t.Fatalf("使用者選過「甚麼」後不再限制：%q", got)
	}
}
