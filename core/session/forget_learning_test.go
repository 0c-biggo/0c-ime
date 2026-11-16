package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 清除學習紀錄（#301）：輸入框留著的前文是清除前打的字，清除後不再拿來選字，下一次送出也不跟它學成相鄰詞。
func TestForgetLearningDropsDocumentContext(t *testing.T) {
	s := documentSession()
	id := contextCommit(s)
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
	s.model.ResetStore()
	s.ForgetLearning()
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "井" {
		t.Fatalf("清除後仍以清除前的「風」當前文：%s", got)
	}
	key(s, proto.KeyEnter)
	for k := range s.model.Store.Bi {
		if k == "風\t井" || k == "風\t景" {
			t.Fatalf("清除前的前文被學成相鄰詞：%q", k)
		}
	}
	if s.model.Store.Uni["井"] == 0 {
		t.Fatal("清除後新打的字要照常學")
	}
}

// 組字途中清除：畫面上那段照舊送出，但它是清除前打的（含清除前的手選），送出時不學；之後的組字照常學。
func TestForgetLearningWhileComposingSkipsThatComposition(t *testing.T) {
	s := documentSession()
	typeSyl(s, "ㄐㄧㄥˇ")
	s.model.ResetStore()
	s.ForgetLearning()
	if got := key(s, proto.KeyEnter).Commit; got != "井" {
		t.Fatalf("組字照常送出：%q", got)
	}
	if st := s.model.Store; st.Total != 0 || len(st.Uni)+len(st.Bi)+len(st.EvidenceByWord)+len(st.EvidenceByContext) != 0 {
		t.Fatalf("清除前開始的組字被學進去：%+v", st)
	}
	typeSyl(s, "ㄈㄥ")
	key(s, proto.KeyEnter)
	if s.model.Store.Uni["風"] == 0 {
		t.Fatal("下一段組字要照常學")
	}
}
