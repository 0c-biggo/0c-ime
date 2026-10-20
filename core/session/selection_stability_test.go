package session

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func selectionStabilitySession(neural bool) *Session {
	lex, sys := fuzzyLexicon(word{"必", "ㄅㄧˋ", .1}, word{"壁", "ㄅㄧˋ", .002}, word{"檢", "ㄐㄧㄢˇ", .1}, word{"剪", "ㄐㄧㄢˇ", .05})
	sys.SetBi("必", "檢", 1)
	sys.SetBi("壁", "剪", .1)
	sys.SetBi("必", "剪", 1e-10)
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	if neural {
		sys.SetUni("壁", .2)
		sys.SetBi("必", "檢", 1e-8)
		s.Rerank = rerank.Options{Weight: .1, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
			scores := make([]float64, len(r.Candidates))
			for i, c := range r.Candidates {
				if c.Text == "必檢" {
					scores[i] = 1000
				}
			}
			return scores, nil
		})}
	}
	return s
}

func TestSelectPreservesUnselectedPrefix(t *testing.T) {
	for _, neural := range []bool{false, true} {
		t.Run(map[bool]string{false: "statistical", true: "neural"}[neural], func(t *testing.T) {
			s := selectionStabilitySession(neural)
			if got := typeReadingLocal(s, "ㄅㄧˋ ㄐㄧㄢˇ").Text(); got != "必檢" {
				t.Fatalf("fixture: %s", got)
			}
			selectText(t, s, 1, "剪")
			if got := s.snapshot().Text(); got != "必剪" {
				t.Fatalf("selecting 剪 changed the prefix: %s", got)
			}
			if _, ok := s.locks[0]; ok {
				t.Fatal("unselected prefix must not become a manual choice")
			}
			for _, r := range []proto.Request{{Type: proto.Cursor, To: 0}, {Type: proto.Cursor, To: 2}, {Type: proto.Candidates, Length: 1}, {Type: proto.Close}} {
				if got := s.Handle(r).Text(); got != "必剪" {
					t.Fatalf("navigation changed selection: %s", got)
				}
			}
			if got := key(s, proto.KeyEnter).Commit; got != "必剪" {
				t.Fatalf("commit changed selection: %s", got)
			}
		})
	}
}

func TestSelectPreservesUnselectedSuffix(t *testing.T) {
	s := selectionStabilitySession(false)
	typeReadingLocal(s, "ㄅㄧˋ ㄐㄧㄢˇ")
	selectText(t, s, 0, "壁")
	if got := s.snapshot().Text(); got != "壁檢" {
		t.Fatal("selecting the first character changed the suffix", got)
	}
}

func TestSelectInsideWordWithoutFragmentEntries(t *testing.T) {
	lex, sys := fuzzyLexicon(word{"必檢", "ㄅㄧˋ ㄐㄧㄢˇ", .2}, word{"剪", "ㄐㄧㄢˇ", .1})
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = true
	if got := typeReadingLocal(s, "ㄅㄧˋ ㄐㄧㄢˇ").Text(); got != "必檢" {
		t.Fatal(got)
	}
	selectText(t, s, 1, "剪")
	if got := s.snapshot().Text(); got != "必剪" {
		t.Fatal("word fragment disappeared", got)
	}
	if got := key(s, proto.KeyEnter).Commit; got != "必剪" {
		t.Fatal(got)
	}
	if s.model.Store.Uni["必"] != 0 {
		t.Fatal("unselected fragment must not be learned as a complete word")
	}
}

func TestSelectDoesNotFreezeFutureInput(t *testing.T) {
	s := selectionStabilitySession(false)
	s.model.Lex.(*lexicon.Mem).Add(lexicon.Entry{Surface: "都", Reading: zhuyin.ParseReading("ㄉㄨ"), PRW: 1})
	s.model.LM.(*lm.Mem).SetUni("都", .1)
	typeReadingLocal(s, "ㄅㄧˋ ㄐㄧㄢˇ")
	selectText(t, s, 1, "剪")
	s.moveTo(2)
	got := typeSyl(s, "ㄉㄨ").Text()
	if !strings.HasSuffix(got, "剪都") || got == "必剪" {
		t.Fatal("new input must still decode", got)
	}
}
