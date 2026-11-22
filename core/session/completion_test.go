package session

import (
	"encoding/json"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// newCompletionSession 是手機補全的測試詞庫：「一路順風」打到第三個音節就該補，
// 「你」在前面讓補全從句中開始。
func newCompletionSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你", "ㄋㄧˇ", 1e-2}, {"一", "ㄧ", 1e-2}, {"路", "ㄌㄨˋ", 1e-3}, {"順", "ㄕㄨㄣˋ", 1e-3}, {"風", "ㄈㄥ", 1e-3},
		{"一路", "ㄧˊ ㄌㄨˋ", 1e-4}, {"一路順風", "ㄧˊ ㄌㄨˋ ㄕㄨㄣˋ ㄈㄥ", 2e-3},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	s := New(personal.NewModel(lex, sys))
	s.Toneless = true
	return s
}

// 沒打聲調時最後一個注音還沒完成，桌面的 Tab 補全不出現；手機在完成之後的複本上算得到。
func TestCompletionOnceThePendingSyllableFinishes(t *testing.T) {
	s := newCompletionSession()
	snap := symbolType(s, "ㄋㄧˇㄧㄌㄨㄕㄨㄣ")
	if snap.Suggestion != "" {
		t.Fatalf("未完成的注音不該有 Tab 補全：%q", snap.Suggestion)
	}
	if g := s.Completion(); g != nil {
		t.Fatalf("注音還沒完成就給了補全 %+v", g)
	}
	before, _ := json.Marshal(s.View())
	saved := *s
	s.Handle(proto.Request{Type: proto.Candidates})
	g := s.Completion()
	*s = saved
	if g == nil || g.Word != "一路順風" || g.Rest != "風" {
		t.Fatalf("補全 %+v", g)
	}
	if after, _ := json.Marshal(s.View()); string(after) != string(before) {
		t.Fatalf("複本上的預覽改到了組字：\n前 %s\n後 %s", before, after)
	}
}

func TestCompleteRequestTakesTheWordAndCommits(t *testing.T) {
	s := newCompletionSession()
	symbolType(s, "ㄋㄧˇㄧㄌㄨㄕㄨㄣ")
	if snap := s.Handle(proto.Request{Type: proto.Complete, Word: "一路順風"}); !snap.Handled {
		t.Fatalf("沒有接受補全：%+v", snap)
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "你一路順風" {
		t.Fatalf("送出 %q", got)
	}
}

// 畫面上的補全已經過時（例如又打了字），引擎算出的不是同一個詞就不接受。
func TestCompleteRequestRefusesAnotherWord(t *testing.T) {
	s := newCompletionSession()
	symbolType(s, "ㄋㄧˇㄧㄌㄨㄕㄨㄣ")
	if snap := s.Handle(proto.Request{Type: proto.Complete, Word: "一路平安"}); snap.Handled {
		t.Fatal("接受了不是目前補全的詞")
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got != "你一路順" {
		t.Fatalf("送出 %q", got)
	}
}

// 游標在組字最後時 → 跟 Tab 一樣接受補全；沒有補全時 → 照舊不動。
func TestRightArrowAcceptsCompletionAtEnd(t *testing.T) {
	s := newCompletionSession()
	s.Toneless = false
	snap := symbolType(s, "ㄋㄧˇㄧˊㄌㄨˋㄕㄨㄣˋ")
	if snap.Suggestion == "" {
		t.Fatalf("應有補全建議：%+v", snap)
	}
	snap = key(s, proto.KeyRight)
	if !snap.Handled || snap.Text() != "你一路順風" || snap.Suggestion != "" {
		t.Fatalf("→ 沒有接受補全：%+v", snap)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "你一路順風" {
		t.Fatalf("送出 %q", got)
	}
}

// 游標不在最後時 → 是移游標，不接受補全。
func TestRightArrowMovesCursorBeforeEnd(t *testing.T) {
	s := newCompletionSession()
	s.Toneless = false
	symbolType(s, "ㄋㄧˇㄧˊㄌㄨˋㄕㄨㄣˋ")
	key(s, proto.KeyLeft)
	snap := key(s, proto.KeyRight)
	if snap.Text() != "你一路順" || snap.Cursor.Rune != 4 {
		t.Fatalf("→ 應只移游標：%+v", snap)
	}
}

// 同音替換預覽只有 Tab 能接受；→ 不改字。
func TestRightArrowDoesNotAcceptHomophoneRewrite(t *testing.T) {
	s := homophoneSession()
	typeXiaolong(s)
	key(s, proto.KeyTab)
	if snap := key(s, proto.KeyRight); snap.Text() != "小龍" {
		t.Fatalf("→ 接受了同音替換：%+v", snap)
	}
}

// 沒有補全時，句尾的 → 照舊吃掉不動作：文字與游標都不變（#338）。
func TestRightArrowAtEndWithoutCompletionKeepsComposition(t *testing.T) {
	s := newCompletionSession()
	s.Toneless = false
	snap := symbolType(s, "ㄋㄧˇ")
	if snap.Suggestion != "" {
		t.Fatalf("不該有補全：%+v", snap)
	}
	after := key(s, proto.KeyRight)
	if !after.Handled || after.Text() != snap.Text() || after.Cursor != snap.Cursor {
		t.Fatalf("→ 改了組字：%+v → %+v", snap, after)
	}
}

// 聲調選填時最後一個音節還沒完成，畫面沒有補全；→ 只把音節收完（顯示補全），不接受還沒看到的補全。
// 補全出現之後再按一次 → 才接受（#338）。
func TestRightArrowDoesNotAcceptACompletionTheUntonedKeyRevealed(t *testing.T) {
	s := newCompletionSession()
	snap := symbolType(s, "ㄋㄧˇㄧㄌㄨㄕㄨㄣ")
	if snap.Suggestion != "" {
		t.Fatalf("未完成的注音不該有補全：%+v", snap)
	}
	snap = key(s, proto.KeyRight)
	if snap.Text() == "你一路順風" {
		t.Fatalf("→ 接受了畫面上沒有的補全：%+v", snap)
	}
	if snap.Suggestion == "" {
		t.Fatalf("收完音節後應出現補全：%+v", snap)
	}
	if snap = key(s, proto.KeyRight); snap.Text() != "你一路順風" {
		t.Fatalf("補全出現後 → 沒有接受：%+v", snap)
	}
}

// 新酷音（Tab 換斷詞）、小麥注音（Tab 換候選）的 Tab 不接受補全，句尾的 → 也不接受（#338）。
func TestRightArrowFollowsTheInputStyleTab(t *testing.T) {
	for _, id := range []string{"chewing", "mcbopomofo"} {
		s := newCompletionSession()
		s.Toneless = false
		p, ok := inputstyle.Get(id)
		if !ok {
			t.Fatal(id)
		}
		s.InputStyle = p.Rules
		snap := symbolType(s, "ㄋㄧˇㄧˊㄌㄨˋㄕㄨㄣˋ")
		if snap.Suggestion == "" {
			t.Fatalf("%s：應有補全建議：%+v", id, snap)
		}
		if snap = key(s, proto.KeyRight); snap.Text() == "你一路順風" {
			t.Fatalf("%s：→ 接受了補全：%+v", id, snap)
		}
	}
}

// 九宮格還有待定按鍵時，→ 不接受補全（原本顯示的補全已經不接在組字最後），免得待定的音節跑到補全詞後面（#338）。
func TestRightArrowDoesNotAcceptWhileNineKeysPend(t *testing.T) {
	s := newCompletionSession()
	s.Toneless = false
	if snap := symbolType(s, "ㄋㄧˇㄧˊㄌㄨˋㄕㄨㄣˋ"); snap.Suggestion == "" {
		t.Fatalf("應有補全建議：%+v", snap)
	}
	press(s, 7)
	if !s.ninePending() || s.sug == nil {
		t.Fatalf("前提不成立：pending=%v sug=%v", s.ninePending(), s.sug)
	}
	snap := key(s, proto.KeyRight) // → 先把待定的九宮格鍵收成音節（照舊），但不接受補全
	if []rune(snap.Text())[len([]rune(snap.Text()))-1] == '風' {
		t.Fatalf("→ 接受了補全：%+v", snap)
	}
	for _, l := range s.locks {
		if l.Completion {
			t.Fatalf("→ 鎖了補全詞：%+v", s.locks)
		}
	}
}
