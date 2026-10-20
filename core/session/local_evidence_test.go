package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func evidenceSession() *Session {
	l, m := lexicon.NewMem(), lm.NewMem()
	for _, v := range []struct {
		w, r string
		p    float64
	}{{"心", "ㄒㄧㄣ", .3}, {"新", "ㄒㄧㄣ", .1}, {"圖", "ㄊㄨˊ", .2}, {"並", "ㄅㄧㄥˋ", .1}, {"意", "ㄧˋ", .2}} {
		l.Add(lexicon.Entry{Surface: v.w, Reading: zhuyin.ParseReading(v.r), PRW: 1})
		m.SetUni(v.w, v.p)
	}
	s := New(personal.NewModel(l, m))
	s.Opt.PrefixAware = false
	return s
}

func selectText(t testing.TB, s *Session, at int, w string) {
	t.Helper()
	s.Handle(proto.Request{Type: proto.Cursor, To: at})
	a := s.Handle(proto.Request{Type: proto.Candidates, Length: len([]rune(w))})
	if a.Candidates != nil {
		for _, v := range a.Candidates.Items {
			if v.Text == w {
				s.Handle(proto.Request{Type: proto.Select, Index: v.ID})
				return
			}
		}
	}
	t.Fatalf("找不到 %s：%+v", w, a)
}

func typeReadingLocal(s *Session, r string) proto.Snapshot {
	var snap proto.Snapshot
	for _, y := range zhuyin.ParseReading(r) {
		snap = typeSyl(s, string(y))
	}
	return snap
}

func TestLocalCorrectionUsesMatchingContextWithoutPersistentLearning(t *testing.T) {
	s := evidenceSession()
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ")
	selectText(t, s, 0, "新")
	if got := s.snapshot().Text(); got != "新圖並新圖" {
		t.Fatal(got)
	}
	if s.model.Store.Total != 0 || s.model.Gen != 0 {
		t.Fatal("尚未提交不得更新持久模型")
	}
	other := New(s.model)
	other.Opt.PrefixAware = false
	if got := typeReadingLocal(other, "ㄒㄧㄣ ㄊㄨˊ").Text(); got != "心圖" {
		t.Fatal("證據外溢到其他 session", got)
	}
	key(s, proto.KeyEnter)
	if s.model.Store.Uni["新"] != 2*s.model.P.WPassive {
		t.Fatal("一般計數各只學一次；強手選另存於條件偏好", s.model.Store.Uni)
	}
}

func TestLocalExplicitCorrectionOutweighsEarlierPassiveChoice(t *testing.T) {
	s := evidenceSession()
	s.model.Store.Ctx["並"] = .25
	s.model.Store.Bi["並\t心"] = .25
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ")
	selectText(t, s, 0, "新")
	if got := s.snapshot().Text(); got != "新圖並新圖" {
		t.Fatal(got)
	}
	if s.model.Store.Bi["並\t心"] != .25 || s.model.Store.Bi["並\t新"] != 0 {
		t.Fatal("只調整本次組字，不提早改掉歷史")
	}
	for _, k := range []string{proto.KeyRight, proto.KeyLeft, proto.KeyRight} {
		if got := key(s, k).Text(); got != "新圖並新圖" {
			t.Fatal("游標移動不能讓暫時證據自我強化或變動", got)
		}
	}
}

func TestLocalCorrectionDoesNotReplaceDifferentContextOrCrossLiteral(t *testing.T) {
	s := evidenceSession()
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄧˋ")
	selectText(t, s, 0, "新")
	if got := s.snapshot().Text(); got != "新圖並心意" {
		t.Fatal(got)
	}
	s = evidenceSession()
	typeReadingLocal(s, "ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄧˋ")
	selectText(t, s, 1, "新")
	if got := s.snapshot().Text(); got != "並新圖並心意" {
		t.Fatal("共同左鄰不能蓋過不同右鄰", got)
	}
	s = evidenceSession()
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ")
	selectText(t, s, 0, "新")
	s.moveTo(len(s.syls))
	s.insertLiteral("，")
	if got := typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ").Text(); got != "新圖，心圖" {
		t.Fatal(got)
	}
}

func TestLocalCorrectionIsRetractedByEditsCancelAndPrivacy(t *testing.T) {
	for _, action := range []string{"delete", "replace", "cancel", "pause", "private"} {
		t.Run(action, func(t *testing.T) {
			s := evidenceSession()
			typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ")
			selectText(t, s, 0, "新")
			switch action {
			case "delete":
				s.Handle(proto.Request{Type: proto.Cursor, To: 1})
				s.Handle(proto.Request{Type: proto.Backspace})
			case "replace":
				selectText(t, s, 0, "心")
			case "cancel":
				s.Handle(proto.Request{Type: proto.Cancel})
				typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ")
			case "pause":
				s.Handle(proto.Request{Type: proto.Pause, On: true})
				s.Handle(proto.Request{Type: proto.Pause, On: false})
			case "private":
				s.Handle(proto.Request{Type: proto.Close, Private: true})
			}
			s.refresh()
			got := []rune(s.snapshot().Text())
			if string(got[len(got)-2:]) != "心圖" {
				t.Fatal(string(got))
			}
			if s.model.Store.Total != 0 {
				t.Fatal("操作不應持久學習")
			}
		})
	}
}

func TestCandidateStartsAtCurrentSegmentRatherThanUnrelatedLongWord(t *testing.T) {
	s := evidenceSession()
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "新圖", Reading: zhuyin.ParseReading("ㄒㄧㄣ ㄊㄨˊ"), PRW: 1})
	s.model.LM.(*lm.Mem).SetUni("新圖", .00001)
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	c := key(s, proto.KeyDown).Candidates
	if c == nil || c.Length != 1 {
		t.Fatalf("應直接重選畫面上的心：%+v", c)
	}
	c = key(s, proto.KeyUp).Candidates
	if c == nil || c.Length != 2 {
		t.Fatalf("較長詞仍須可選：%+v", c)
	}
	if a := key(s, proto.KeyTab); a.Candidates == nil || a.Candidates.Length != 1 || a.Commit != "" {
		t.Fatal("Tab 只切詞長", a)
	}
	if a := key(s, proto.KeyTab); a.Candidates == nil || a.Candidates.Length != 2 || a.Commit != "" {
		t.Fatal(a)
	}
}

func TestCursorMovementDoesNotRewritePrefix(t *testing.T) {
	s := learningSession()
	want := typeSyl(s, "ㄇㄚˊ").Text()
	if want != "麻" {
		t.Fatal(want)
	}
	for _, k := range []string{proto.KeyLeft, proto.KeyRight, proto.KeyLeft} {
		if got := key(s, k).Text(); got != want {
			t.Fatalf("%s 只移動游標，不應把 %s 改成 %s", k, want, got)
		}
	}
	if got := key(s, proto.KeyEnter).Commit; got != want {
		t.Fatal(got)
	}
}
