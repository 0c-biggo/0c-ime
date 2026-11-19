package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

func shownSession() *Session {
	s := newSession()
	s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true
	typeSymbols(s, "ㄈㄥㄐㄧㄥㄨㄛ")
	return s
}

// What the strip shows is sent: as decoded, or split again into the best words for
// that text, learned as an ordinary commit and not as picks.
func TestCommitShown(t *testing.T) {
	s := shownSession()
	if v := s.Handle(proto.Request{Type: proto.Commit, Word: "風景我"}); !v.Handled || v.Commit != "風景我" {
		t.Fatalf("as decoded: %+v", v)
	}
	s = shownSession()
	v := s.Handle(proto.Request{Type: proto.Commit, Word: "風井我"})
	if !v.Handled || v.Commit != "風井我" || v.Text() != "" {
		t.Fatalf("another text: %+v", v)
	}
	if s.model.Store.Bi["風\t井"] == 0 {
		t.Fatalf("not learned as sent: %v", s.model.Store.Bi)
	}
	if len(s.model.Store.Readings) != 0 || len(s.model.Store.Picked) != 0 || len(s.locks) != 0 {
		t.Fatalf("learned as picks: readings %v picked %v locks %v", s.model.Store.Readings, s.model.Store.Picked, s.locks)
	}
}

// What cannot be sent as shown is not sent: the host commits as before.
func TestCommitShownRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(s *Session)
		text  string
	}{
		"a character the reading cannot be": {func(*Session) {}, "風景你"},
		"another length":                    {func(*Session) {}, "風景"},
		"a word picked":                     {func(s *Session) { s.Handle(proto.Request{Type: proto.Fix, To: 0, Length: 2, Word: "風景"}) }, "風井我"},
	} {
		t.Run(name, func(t *testing.T) {
			s := shownSession()
			c.setup(s)
			if name == "a word picked" && len(s.locks) == 0 {
				t.Fatal("fixture: the word was not fixed")
			}
			v := s.Handle(proto.Request{Type: proto.Commit, Word: c.text})
			if v.Handled || v.Commit != "" || v.Text() == "" {
				t.Fatalf("%+v", v)
			}
			if len(s.model.Store.Bi) != 0 {
				t.Fatalf("learned %v", s.model.Store.Bi)
			}
			if v := s.Handle(proto.Request{Type: proto.Commit}); v.Commit == "" {
				t.Fatalf("plain commit after: %+v", v)
			}
		})
	}
}
