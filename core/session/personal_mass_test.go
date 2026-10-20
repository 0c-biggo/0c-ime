package session

import (
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"testing"
)

// Matches the structural failure: a common first word, a low-frequency reading
// of its likely successor, and an otherwise preferred complete word.
func TestPassiveFirstWordDoesNotBreakCompleteWord(t *testing.T) {
	lex, sys := lexicon.NewMem(), lm.NewMem()
	for _, x := range []struct {
		w, r  string
		p, rw float64
	}{
		{"切", "ㄑㄧㄝ", 3e-5, 1}, {"入", "ㄖㄨˋ", 9e-5, 1}, {"肉", "ㄖㄨˋ", 4.7e-5, .1}, {"切入", "ㄑㄧㄝ ㄖㄨˋ", 1.4e-5, 1},
	} {
		lex.Add(lexicon.Entry{Surface: x.w, Reading: zhuyin.ParseReading(x.r), PRW: x.rw})
		sys.SetUni(x.w, x.p)
	}
	sys.SetBi("切", "肉", .004)
	sys.SetBi("切", "入", .00025)
	m := personal.NewModel(lex, sys)
	s := New(m)
	check := func() {
		t.Helper()
		typeSyl(s, "ㄑㄧㄝ")
		got := typeSyl(s, "ㄖㄨˋ")
		if got.Text() != "切入" {
			t.Fatalf("ordinary learning changed complete word: %s", got.Text())
		}
		key(s, proto.KeyEscape)
	}
	check()
	for i := 0; i < 100; i++ {
		typeSyl(s, "ㄑㄧㄝ")
		key(s, proto.KeyEnter)
		if i == 0 || i == 99 {
			check()
		}
	}
	// High existing counts must not make the unseen complete word lose through
	// a different mixture denominator, even when no new typing is involved.
	for i := 100; i < 10000; i++ {
		m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄑㄧㄝ"), Path: []personal.Seg{{Word: "切", Start: 0, End: 1}}})
	}
	check()
	// A legacy profile containing only the old aggregate counts is protected too.
	m.Store.Corrections = nil
	m.Store.EvidenceByContext = nil
	m.Store.EvidenceByWord = nil
	check()
	// Repeated deletion/retyping remains correct without recording rejections.
	typeSyl(s, "ㄑㄧㄝ")
	for i := 0; i < 3; i++ {
		got := typeSyl(s, "ㄖㄨˋ")
		if got.Text() != "切入" {
			t.Fatal(got.Text())
		}
		key(s, proto.KeyBackspace)
	}
}
