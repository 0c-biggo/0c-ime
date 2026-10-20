package session

import (
	"errors"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/punct"
)

type punctFunc func(string) ([]punct.Edit, error)

func (f punctFunc) Punctuate(text string) ([]punct.Edit, error) { return f(text) }

func TestCommitPunctuationInsertsOnlyWhenAllowed(t *testing.T) {
	var seen []string
	edits, fail := []punct.Edit{{Offset: 1, Text: "，"}}, error(nil)
	s := newSession()
	s.Punct = punctFunc(func(text string) ([]punct.Edit, error) { seen = append(seen, text); return edits, fail })
	commit := func(private bool) string {
		for _, y := range []string{"ㄨㄛˇ", "ㄒㄧㄤˇ", "ㄔ"} {
			typeSyl(s, y)
		}
		return s.Handle(proto.Request{Type: proto.Commit, Private: private}).Commit
	}
	if got := commit(false); got != "我，想吃" || len(seen) != 1 || seen[0] != "我想吃" {
		t.Fatalf("commit = %q seen=%v", got, seen)
	}
	if got := commit(true); got != "我想吃" || len(seen) != 1 {
		t.Fatalf("private commit must not be sent: %q seen=%v", got, seen)
	}
	s.Handle(proto.Request{Type: proto.Pause, On: true})
	if got := commit(false); got != "我想吃" || len(seen) != 1 {
		t.Fatalf("paused commit must not be sent: %q", got)
	}
	s.Handle(proto.Request{Type: proto.Pause, On: false})
	fail = errors.New("timeout")
	if got := commit(false); got != "我想吃" {
		t.Fatalf("scorer failure must keep text: %q", got)
	}
	fail, edits = nil, []punct.Edit{{Offset: 3, Text: "。"}} // 段尾不補
	if got := commit(false); got != "我想吃" {
		t.Fatalf("invalid edit must keep text: %q", got)
	}
}
