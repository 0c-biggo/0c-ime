package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/proto"
)

func learningSession() *Session {
	s := newSession()
	s.model.LM.(*lm.Mem).SetUni("麻煩", 0.01)
	return s
}

func TestCompletionLearningKeepsSourceUntilCommit(t *testing.T) {
	s := learningSession()
	s.Opt.Suggest.AttentionCost = 0
	if snap := typeSyl(s, "ㄇㄚˊ"); snap.Suggestion != "煩" {
		t.Fatalf("測試需要麻煩的補全：%+v", snap)
	}
	key(s, proto.KeyTab)
	if s.model.Evidence(lm.BOS, "麻煩") != 0 {
		t.Fatal("接受但尚未提交不得學習")
	}
	key(s, proto.KeyEnter)
	if got := s.model.Store.Bi[lm.BOS+"\t麻煩"]; got != s.model.P.WCompletion {
		t.Fatalf("Tab 提交應保留補全權重：%g", got)
	}

	s = learningSession()
	s.Opt.Suggest.AttentionCost = 0
	typeSyl(s, "ㄇㄚˊ")
	key(s, proto.KeyTab)
	key(s, proto.KeyEscape)
	if s.model.Evidence(lm.BOS, "麻煩") != 0 {
		t.Fatal("取消補全不得留下正向學習")
	}
}

func TestIgnoredSuggestionEqualsTypingWithoutSuggestions(t *testing.T) {
	a, b := learningSession(), learningSession()
	b.Opt.Suggest.MinConf = 2
	for _, s := range []*Session{a, b} {
		typeSyl(s, "ㄇㄚˊ")
		typeSyl(s, "ㄈㄢˊ")
		key(s, proto.KeyEnter)
	}
	if x, y := a.model.Evidence(lm.BOS, "麻煩"), b.model.Evidence(lm.BOS, "麻煩"); x != y || x <= 0 {
		t.Fatalf("忽略建議不應被當成拒絕或接受：%g vs %g", x, y)
	}
}

func TestSessionUsesInjectedClock(t *testing.T) {
	s := learningSession()
	now := int64(100 * 86400)
	s.model.Now = func() int64 { return now }
	typeSyl(s, "ㄇㄚˊ")
	typeSyl(s, "ㄈㄢˊ")
	key(s, proto.KeyEnter)
	before := s.model.Evidence(lm.BOS, "麻煩")
	now += 30 * 86400
	typeSyl(s, "ㄇㄚˊ")
	if s.model.Evidence(lm.BOS, "麻煩") >= before {
		t.Fatal("回到輸入欄位時應更新時間，即使還沒有提交")
	}
}

func TestPrivateCompositionNeverLearnsAfterLeavingField(t *testing.T) {
	s := learningSession()
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄇㄚˊ", Private: true})
	s.Handle(proto.Request{Type: proto.Symbol, Symbol: "ㄈㄢˊ", Private: true})
	if snap := key(s, proto.KeyEnter); snap.Commit != "麻煩" {
		t.Fatalf("提交應不受影響：%+v", snap)
	}
	if s.model.Evidence(lm.BOS, "麻煩") != 0 {
		t.Fatal("敏感組字離開欄位後不能學入個人資料")
	}
	typeSyl(s, "ㄇㄚˊ")
	typeSyl(s, "ㄈㄢˊ")
	key(s, proto.KeyEnter)
	if s.model.Evidence(lm.BOS, "麻煩") <= 0 {
		t.Fatal("下一次一般組字應正常學習")
	}
}
