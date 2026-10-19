package session

import (
	"testing"

	"git.biggo.com/0c/ime/core/decode"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

func homophoneSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, x := range []struct {
		w, r string
		p    float64
	}{
		{"小", "ㄒㄧㄠˇ", .01}, {"龍", "ㄌㄨㄥˊ", .01}, {"小龍", "ㄒㄧㄠˇ ㄌㄨㄥˊ", .6},
		{"小籠包", "ㄒㄧㄠˇ ㄌㄨㄥˊ ㄅㄠ", .1}, {"包", "ㄅㄠ", .01}, {"我", "ㄨㄛˇ", .1},
	} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
		sys.SetUni(x.w, x.p)
	}
	return New(personal.NewModel(lex, sys))
}

func typeXiaolong(s *Session) proto.Snapshot {
	typeSyl(s, "ㄒㄧㄠˇ")
	return typeSyl(s, "ㄌㄨㄥˊ")
}

func TestHomophoneTabPreviewsThenAccepts(t *testing.T) {
	s := homophoneSession()
	snap := typeXiaolong(s)
	if snap.Text() != "小龍" || snap.Suggestion != "" {
		t.Fatalf("原組字不應被改寫：%+v", snap)
	}
	snap = key(s, proto.KeyTab)
	if !snap.Handled || snap.Text() != "小龍" || snap.Commit != "" || snap.Suggestion != "小龍 → 小籠包" || snap.SuggestedText() != "小籠包" {
		t.Fatalf("先預覽而不替換：%+v", snap)
	}
	snap = key(s, proto.KeyTab)
	if snap.Text() != "小籠包" || snap.Cursor.Rune != 3 || snap.SuggestionEdit != nil || snap.Commit != "" {
		t.Fatal(snap)
	}
	if s.locks[0] != (decode.Lock{Word: "小籠包", Len: 3, Completion: true}) {
		t.Fatal(s.locks)
	}
	if key(s, proto.KeyEnter).Commit != "小籠包" {
		t.Fatal("Enter 提交須符合接受後畫面")
	}
}

func TestHomophonePreviewDoesNotChangeEnterOrIgnoredTyping(t *testing.T) {
	s := homophoneSession()
	typeXiaolong(s)
	key(s, proto.KeyTab)
	if got := key(s, proto.KeyEnter).Commit; got != "小龍" {
		t.Fatalf("不能提交未接受預覽：%s", got)
	}
	typeXiaolong(s)
	key(s, proto.KeyTab)
	if got := key(s, proto.KeyEscape); got.Text() != "小龍" || got.SuggestionEdit != nil {
		t.Fatal(got)
	}
	if got := key(s, proto.KeyEscape); got.Text() != "" {
		t.Fatal(got)
	}
	for _, action := range []string{proto.KeyLeft, proto.KeyBackspace, "v"} {
		s, baseline := homophoneSession(), homophoneSession()
		typeXiaolong(s)
		typeXiaolong(baseline)
		key(s, proto.KeyTab)
		a, b := key(s, action), key(baseline, action)
		if a.Text() != b.Text() || a.SuggestionEdit != nil {
			t.Fatalf("忽略預覽後 %s: %+v != %+v", action, a, b)
		}
	}
}

func TestHomophoneCompletionNeverOverwritesManualChoice(t *testing.T) {
	for _, lockSingle := range []bool{false, true} {
		s := homophoneSession()
		typeXiaolong(s)
		if lockSingle {
			s.locks[1] = decode.Lock{Word: "龍", Len: 1}
		} else {
			s.locks[0] = decode.Lock{Word: "小龍", Len: 2}
		}
		s.refresh()
		snap := key(s, proto.KeyTab)
		if !snap.Handled || snap.SuggestionEdit != nil || snap.Text() != "小龍" { // 組字中吃掉不動作（#106）
			t.Fatal(snap)
		}
	}
}

func TestHomophonePreviewAfterRawZhuyinUsesDisplayOffsets(t *testing.T) {
	s := homophoneSession()
	s.insertLiteral("ㄅㄨㄛ")
	typeXiaolong(s)
	snap := key(s, proto.KeyTab)
	if snap.SuggestionEdit == nil || snap.SuggestionEdit.Start != 3 || snap.SuggestedText() != "ㄅㄨㄛ小籠包" {
		t.Fatal(snap)
	}
	if got := key(s, proto.KeyTab).Text(); got != "ㄅㄨㄛ小籠包" {
		t.Fatal(got)
	}
}

func TestCompletionPreservesEarlierLockAndDoesNotLearnPrivateOrPaused(t *testing.T) {
	for _, mode := range []string{"private", "pause"} {
		s := homophoneSession()
		if mode == "pause" {
			s.Handle(proto.Request{Type: proto.Pause, On: true})
		}
		typeSyl(s, "ㄨㄛˇ")
		s.locks[0] = decode.Lock{Word: "我", Len: 1}
		typeXiaolong(s)
		if mode == "private" {
			s.Handle(proto.Request{Type: proto.Close, Private: true})
		}
		key(s, proto.KeyTab)
		snap := key(s, proto.KeyTab)
		if snap.Text() != "我小籠包" || s.locks[0].Word != "我" {
			t.Fatal(snap, s.locks)
		}
		key(s, proto.KeyEnter)
		if s.model.Store.Total != 0 {
			t.Fatalf("%s 不應學習", mode)
		}
	}
}

func TestCommittedTextDoesNotLeakIntoAnotherComposition(t *testing.T) {
	s := homophoneSession()
	typeXiaolong(s)
	key(s, proto.KeyTab)
	key(s, proto.KeyTab)
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	key(s, proto.KeyEnter)
	other := homophoneSession()
	a, b := typeXiaolong(s), typeXiaolong(other)
	if a.Text() != b.Text() || a.Suggestion != b.Suggestion || a.SuggestionEdit != nil {
		t.Fatal(a, b)
	}
}
