package session

import (
	"encoding/json"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
	"math"
	"testing"
)

func documentSession() *Session {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, x := range []struct {
		w, r string
		p    float64
	}{{"風", "ㄈㄥ", .01}, {"景", "ㄐㄧㄥˇ", .001}, {"井", "ㄐㄧㄥˇ", .01}} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
		sys.SetUni(x.w, x.p)
	}
	sys.SetBi("風", "景", .9)
	s := New(personal.NewModel(lex, sys))
	s.Opt.PrefixAware = false
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	return s
}
func contextCommit(s *Session) uint64 {
	typeSyl(s, "ㄈㄥ")
	return key(s, proto.KeyEnter).ContextID
}
func TestDocumentContextRequiresConfirmation(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		s := documentSession()
		id := contextCommit(s)
		if id == 0 {
			t.Fatal("missing token")
		}
		before, _ := json.Marshal(s.model.Store)
		if confirm {
			s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
		}
		after, _ := json.Marshal(s.model.Store)
		if string(before) != string(after) {
			t.Fatal("context confirmation learned document text")
		}
		want := "井"
		if confirm {
			want = "景"
		}
		if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != want {
			t.Fatalf("confirmed=%v got %s", confirm, got)
		}
		s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
		if s.cand.items[0].Entry.Surface != want {
			t.Fatal("candidate and decode context differ")
		}
	}
}
func TestDocumentContextInvalidationAndIsolation(t *testing.T) {
	for _, action := range []proto.Request{
		{Type: proto.Context}, {Type: proto.Cancel}, {Type: proto.Pause, On: true}, {Type: proto.Key, Key: proto.KeyLeft},
		{Type: proto.Mode, On: false}, {Type: proto.Context, On: true, ContextID: 999}, {Type: proto.Context, Private: true},
	} {
		s := documentSession()
		id := contextCommit(s)
		s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
		s.Handle(action)
		if s.documentLeft != "" || s.pendingLeft != "" {
			t.Fatalf("did not clear %+v", action)
		}
	}
	s := documentSession()
	id := contextCommit(s)
	other := New(s.model)
	other.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
	if other.documentLeft != "" {
		t.Fatal("token crossed session")
	}
	typeSyl(s, "ㄐㄧㄥˇ")
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
	if s.documentLeft != "" {
		t.Fatal("late confirmation changed in-progress input")
	}
}
func TestDocumentContextDoesNotCrossPunctuationOrPersist(t *testing.T) {
	s := documentSession()
	id := contextCommit(s)
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
	typeSyl(s, "ㄐㄧㄥˇ")
	key(s, "<")
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "景，井" {
		t.Fatal(got)
	}
	key(s, proto.KeyEnter)
	if s.model.Store.Bi["風\t景"] != 0 {
		t.Fatal("inferred cross-commit learning")
	}
	typeSyl(s, "ㄈㄥ")
	key(s, "<")
	if snap := key(s, proto.KeyEnter); snap.ContextID != 0 {
		t.Fatal("punctuation left a context token")
	}
	legacy := newSession()
	typeSyl(legacy, "ㄨㄛˇ")
	if key(legacy, proto.KeyEnter).ContextID != 0 {
		t.Fatal("legacy client changed")
	}
}

func TestDocumentContextFeedsCompletionAndExplanation(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		s := documentSession()
		lex := s.model.Lex.(*lexicon.Mem)
		sys := s.model.LM.(*lm.Mem)
		for _, x := range []struct {
			w, r string
			p    float64
		}{{"景色", "ㄐㄧㄥˇ ㄙㄜˋ", .001}, {"景點", "ㄐㄧㄥˇ ㄉㄧㄢˇ", .01}} {
			lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: 1})
			sys.SetUni(x.w, x.p)
		}
		sys.SetBi("風", "景色", .9)
		sys.SetBi("風", "景", .1)
		// 固定目前已顯示的字，單獨測前文對補全的影響。
		sys.SetUni("景", .002)
		sys.SetUni("井", .0001)
		id := contextCommit(s)
		if confirm {
			s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
		}
		snap := typeSyl(s, "ㄐㄧㄥˇ")
		want := "點"
		if confirm {
			want = "色"
		}
		if snap.Suggestion != want {
			c, ok := s.dec.Complete(s.lattice(), s.path)
			t.Fatalf("confirmed=%v suggestion=%q text=%s left=%s complete=%+v ok=%v token=%d", confirm, snap.Suggestion, snap.Text(), s.dec.Left, c, ok, id)
		}
		explanation := s.dec.ExplainBest(s.lattice(), s.locks, false)
		left := lm.BOS
		if confirm && (explanation.DocumentContext == nil || explanation.DocumentContext.Left != "風") {
			t.Fatal("missing context mixture explanation")
		}
		if explanation.Segments[0].Left != left {
			t.Fatal("explanation used a different context")
		}
	}
}

func TestDocumentMixturePreservesPersonalMassAndInteriorPairs(t *testing.T) {
	s := documentSession()
	m := s.model
	// 模擬既有頻率；確認混合保留各自的 Ctx 分母，而非把字數當詞頻。
	m.Store.Bi[lm.BOS+"\t井"] = 10
	m.Store.Ctx[lm.BOS] = 10
	m.Store.Bi["風\t景"] = 3
	m.Store.Ctx["風"] = 3
	scorer := &documentScorer{Model: m, left: "風", weight: .1}
	for _, word := range []string{"景", "井"} {
		for _, pair := range []struct{ got, want float64 }{
			{scorer.Prob(lm.BOS, word), .9*m.Prob(lm.BOS, word) + .1*m.Prob("風", word)},
			{scorer.PriorMass(lm.BOS, word), .9*m.PriorMass(lm.BOS, word) + .1*m.PriorMass("風", word)},
			{scorer.SystemProb(lm.BOS, word), .9*m.SystemProb(lm.BOS, word) + .1*m.SystemProb("風", word)},
			{scorer.Evidence(lm.BOS, word), .9*m.Evidence(lm.BOS, word) + .1*m.Evidence("風", word)},
			{scorer.Prob("風", word), m.Prob("風", word)},
		} {
			if math.Abs(pair.got-pair.want) > 1e-15 {
				t.Fatalf("mixture differs: %+v", pair)
			}
		}
	}
	before, _ := json.Marshal(m.Store)
	s.documentLeft = "風"
	for _, weight := range []float64{0, -1, 2, math.NaN(), math.Inf(1)} {
		s.Opt.DocumentContextWeight = weight
		s.configureDocumentContext(0)
		if s.dec.Score != m {
			t.Fatalf("invalid/zero weight %v must bypass adapter", weight)
		}
	}
	s.Opt.DocumentContextWeight = .1
	s.configureDocumentContext(1)
	if s.dec.Score != m {
		t.Fatal("context crossed punctuation boundary")
	}
	after, _ := json.Marshal(m.Store)
	if string(before) != string(after) {
		t.Fatal("mixture changed store")
	}
}
func TestDocumentWeightChangeRefreshesUnchangedReading(t *testing.T) {
	s := documentSession()
	id := contextCommit(s)
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
	if typeSyl(s, "ㄐㄧㄥˇ").Text() != "景" {
		t.Fatal("missing initial context")
	}
	s.Opt.DocumentContextWeight = 0
	snap := s.Handle(proto.Request{Type: proto.Cursor, To: 1})
	if snap.Text() != "井" {
		t.Fatal("stale decoding after weight change")
	}
}

// 使用者關閉前文時，即使外殼確認同一個代號，也不得以前文選字。
func TestDocumentContextSettingOffIgnoresConfirmation(t *testing.T) {
	s := documentSession()
	s.DocumentContextOff = true
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	id := contextCommit(s)
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: id})
	if got := typeSyl(s, "ㄐㄧㄥˇ").Text(); got != "井" {
		t.Fatalf("前文關閉時仍使用前文：%s", got)
	}
}
