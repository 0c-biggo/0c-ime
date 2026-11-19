package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func predictSession(t *testing.T) *Session {
	t.Helper()
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, e := range []struct {
		w, r string
		p    float64
	}{
		{"你好", "ㄋㄧˇ ㄏㄠˇ", 1e-3}, {"嗎", "ㄇㄚ˙", 1e-3}, {"啊", "ㄚ˙", 5e-4}, {"呢", "ㄋㄜ˙", 4e-4},
		{"朋友", "ㄆㄥˊ ㄧㄡˇ", 2e-4}, {"們", "ㄇㄣ˙", 1e-4},
	} {
		lex.Add(lexicon.Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
		sys.SetUni(e.w, e.p)
	}
	sys.SetBi("你好", "嗎", 0.3)
	sys.SetBi("你好", "啊", 0.1)
	sys.SetBi("你好", "朋友", 0.05)
	sys.SetBi("你好", "ㄅ", 0.5) // not a word of Han characters: never offered
	sys.SetBi("朋友", "們", 0.4)
	s := New(personal.NewModel(lex, sys))
	s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
	return s
}

// commitConfirmed types a reading, sends it and confirms it landed before the caret.
func commitConfirmed(t *testing.T, s *Session, keys string) {
	t.Helper()
	typeSymbols(s, keys)
	v := s.Handle(proto.Request{Type: proto.Commit})
	if v.Commit == "" || v.ContextID == 0 {
		t.Fatalf("no commit to confirm: %+v", v)
	}
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: v.ContextID})
}

func predicted(v proto.Snapshot) []string {
	if v.Candidates == nil {
		return nil
	}
	var out []string
	for _, c := range v.Candidates.Items {
		out = append(out, c.Text)
	}
	return out
}

func TestPredictAfterConfirmedCommit(t *testing.T) {
	s := predictSession(t)
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	v := s.Handle(proto.Request{Type: proto.Predict})
	if got := predicted(v); !slices.Equal(got, []string{"嗎", "啊", "朋友"}) {
		t.Fatalf("predictions %v", got)
	}
	// Picking sends the word, which is the context for the next predictions once confirmed.
	v = s.Handle(proto.Request{Type: proto.Select, Index: 2})
	if v.Commit != "朋友" || v.ContextID == 0 || v.Text() != "" {
		t.Fatalf("pick: %+v", v)
	}
	s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: v.ContextID})
	if got := predicted(s.Handle(proto.Request{Type: proto.Predict})); !slices.Equal(got, []string{"們"}) {
		t.Fatalf("chained predictions %v", got)
	}
}

// Without a confirmed word before the caret the strip offers words to start with instead.
func TestStartWordsWithoutConfirmedContext(t *testing.T) {
	cases := map[string]func(s *Session){
		"nothing sent yet": func(s *Session) {},
		"sent, not confirmed": func(s *Session) {
			typeSymbols(s, "ㄋㄧˇㄏㄠˇ")
			s.Handle(proto.Request{Type: proto.Commit})
		},
		"confirmed, then cleared": func(s *Session) {
			commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
			s.Handle(proto.Request{Type: proto.Context, On: false})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			s := predictSession(t)
			setup(s)
			v := s.Handle(proto.Request{Type: proto.Predict})
			if got := predicted(v); !slices.Equal(got, startWords) || v.Candidates.Kind != predictionKindStart {
				t.Fatalf("start words %v (kind %q)", got, v.Candidates.Kind)
			}
		})
	}
	s := predictSession(t)
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	if v := s.Handle(proto.Request{Type: proto.Predict}); v.Candidates.Kind != "" {
		t.Fatalf("predictions after a word marked %q", v.Candidates.Kind)
	}
}

func TestNoPredictionWhileComposingOrPrivate(t *testing.T) {
	cases := map[string]func(s *Session){
		"composing": func(s *Session) { typeSymbols(s, "ㄋ") },
		"confirmed, then composing": func(s *Session) {
			commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
			typeSymbols(s, "ㄋ")
		},
		"confirmed, then private": func(s *Session) {
			commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
			s.Handle(proto.Request{Type: proto.Close, Private: true})
		},
		"private": func(s *Session) {},
		"paused":  func(s *Session) { s.paused = true },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			s := predictSession(t)
			setup(s)
			s.Handle(proto.Request{Type: proto.Predict, Private: strings.Contains(name, "private")})
			if s.predictions != nil {
				t.Fatalf("predictions %v", s.predictions)
			}
		})
	}
}

// The words the user often sends first come before the default ones; a word sent first only once does not.
func TestStartWordsFollowPersonalUse(t *testing.T) {
	s := predictSession(t)
	w := s.model.P.WPassive
	s.model.Store.Bi["<s>\t朋友"] = 5 * w
	s.model.Store.Bi["<s>\t你好"] = 3 * w
	s.model.Store.Bi["<s>\t們"] = 1 * w       // once: not yet
	s.model.Store.Bi["<s>\tㄅ"] = 9 * w       // not Han characters
	s.model.Store.Bi["<s>\t今天晚上吃什麼"] = 9 * w // a long phrase is not a start word
	s.model.Store.Bi["<s>\t謝謝"] = 4 * w
	s.model.Store.Tombstones["我"] = true
	got := predicted(s.Handle(proto.Request{Type: proto.Predict}))
	want := []string{"朋友", "謝謝", "你好", "你", "好", "對", "那", "這"}
	if !slices.Equal(got, want) {
		t.Fatalf("start words %v, want %v", got, want)
	}
}

// A start word picked is sent, learned as a first word, and predictions follow it once confirmed.
func TestPickedStartWord(t *testing.T) {
	s := predictSession(t)
	got := predicted(s.Handle(proto.Request{Type: proto.Predict}))
	id := slices.Index(got, "你")
	v := s.Handle(proto.Request{Type: proto.Select, Index: id})
	if v.Commit != "你" || v.ContextID == 0 {
		t.Fatalf("pick: %+v", v)
	}
	if c := s.model.Store.Bi["<s>\t你"]; c != s.model.P.WPassive {
		t.Fatalf("learned %v as a first word", c)
	}
	for range 2 {
		s.Handle(proto.Request{Type: proto.Context, On: false})
		got := predicted(s.Handle(proto.Request{Type: proto.Predict}))
		s.Handle(proto.Request{Type: proto.Select, Index: slices.Index(got, "你")})
	}
	s.Handle(proto.Request{Type: proto.Context, On: false})
	if got := predicted(s.Handle(proto.Request{Type: proto.Predict})); got[0] != "你" {
		t.Fatalf("after picking 你 three times: %v", got)
	}
	if c := s.model.Store.Bi["<s>\t你"]; c != 3*s.model.P.WPassive {
		t.Fatalf("learned %v after three picks", c)
	}
	// Undo takes back what the pick learned.
	if v := s.Handle(proto.Request{Type: proto.UndoLearn}); !v.Handled {
		t.Fatal("nothing to undo")
	}
}

// What the user wrote after the word comes before what the dictionary expects.
func TestPredictionsFollowPersonalUse(t *testing.T) {
	s := predictSession(t)
	s.model.Store.Bi["你好\t呢"] = 50
	s.model.Store.Ctx["你好"] = 50
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	if got := predicted(s.Handle(proto.Request{Type: proto.Predict})); len(got) == 0 || got[0] != "呢" {
		t.Fatalf("predictions %v", got)
	}
}

// Predictions are good for the next request only: a select after anything else
// is an ordinary selection, and sends nothing with no composition.
func TestPredictionsLastOneRequest(t *testing.T) {
	s := predictSession(t)
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	s.Handle(proto.Request{Type: proto.Predict})
	s.Handle(proto.Request{Type: proto.Close})
	if v := s.Handle(proto.Request{Type: proto.Select, Index: 0}); v.Commit != "" || !slices.Contains(v.Notices, "沒有這個候選") {
		t.Fatalf("stale prediction sent: %+v", v)
	}
	s.Handle(proto.Request{Type: proto.Predict})
	if v := s.Handle(proto.Request{Type: proto.Select, Index: 9}); v.Commit != "" || !slices.Contains(v.Notices, "沒有這個候選") {
		t.Fatalf("out of range: %+v", v)
	}
}

// A word typed without tones is context like any other: the commit offers it, and
// predictions follow it once confirmed.
func TestTonelessCommitIsContext(t *testing.T) {
	for _, keys := range []string{"ㄋㄧㄏㄠ", "ㄋㄏ"} {
		s := predictSession(t)
		s.Toneless, s.Abbreviation, s.MixedWithToneless = true, true, true
		typeSymbols(s, keys)
		v := s.Handle(proto.Request{Type: proto.Commit})
		if v.Commit != "你好" || v.ContextID == 0 {
			t.Fatalf("%s: sent %q with context %d", keys, v.Commit, v.ContextID)
		}
		s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: v.ContextID})
		if got := predicted(s.Handle(proto.Request{Type: proto.Predict})); len(got) == 0 {
			t.Fatalf("%s: no predictions", keys)
		}
	}
}

// What the user picks after a word comes first next time, and keeps its place.
func TestPickedPredictionMovesUp(t *testing.T) {
	s := predictSession(t)
	pick := func() []string {
		commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
		v := s.Handle(proto.Request{Type: proto.Predict})
		got := predicted(v)
		id := slices.Index(got, "朋友")
		if id < 0 {
			t.Fatalf("朋友 not offered: %v", got)
		}
		s.Handle(proto.Request{Type: proto.Select, Index: id})
		return got
	}
	if first := pick(); first[0] == "朋友" {
		t.Fatalf("fixture: 朋友 should not start first: %v", first)
	}
	for range 3 {
		pick()
	}
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	if got := predicted(s.Handle(proto.Request{Type: proto.Predict})); got[0] != "朋友" {
		t.Fatalf("after picking 朋友: %v", got)
	}
}

// A paused session sends the prediction but learns nothing from it.
func TestPausedPredictionIsNotLearned(t *testing.T) {
	s := predictSession(t)
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	s.Handle(proto.Request{Type: proto.Predict})
	before := len(s.model.Store.Bi)
	s.paused = true
	if v := s.Handle(proto.Request{Type: proto.Select, Index: 0}); v.Commit == "" {
		t.Fatalf("not sent: %+v", v)
	}
	if len(s.model.Store.Bi) != before {
		t.Fatal("learned while paused")
	}
}

// 「不再推薦」（Forget）的詞不列成聯想詞：開頭詞本來就排除，接在前詞後面的聯想詞也要（系統與個人詞對都是）。
func TestForgottenWordIsNotPredicted(t *testing.T) {
	s := predictSession(t)
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	s.Handle(proto.Request{Type: proto.Forget, Word: "嗎"})
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	if got := predicted(s.Handle(proto.Request{Type: proto.Predict})); len(got) == 0 || slices.Contains(got, "嗎") {
		t.Fatalf("predictions after forgetting 嗎: %v", got)
	}
}
