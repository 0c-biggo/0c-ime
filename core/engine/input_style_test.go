package engine

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/inputstyle"
	"github.com/0c-biggo/0c-ime/core/proto"
)

func TestMicrosoftIdleKeysDoNotClaimRawHalfwidthPunctuation(t *testing.T) {
	e := testEngine()
	p, _ := inputstyle.Get("microsoft")
	e.InputStyle = p.Rules
	if strings.ContainsAny(e.IdleKeys(), "\\'\"<>?") {
		t.Fatalf("TestKeyDown must not claim halfwidth pass-through keys: %q", e.IdleKeys())
	}
	if !strings.Contains(e.IdleKeys(), "`") {
		t.Fatal("the prefix must still reach the core")
	}
}

func styleDo(t *testing.T, e *Engine, id uint64, r proto.Request) proto.Snapshot {
	t.Helper()
	s, err := e.Do(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStyleWidthIsolatedAcrossEngineSessions(t *testing.T) {
	e := testEngine()
	p, _ := inputstyle.Get("microsoft")
	e.InputStyle = p.Rules
	e.SharedMode = true
	e.StartEnglish = true
	a, b := e.OpenSession(), e.OpenSession()
	styleDo(t, e, a, proto.Request{Type: proto.Mode, On: true})
	styleDo(t, e, a, proto.Request{Type: proto.Key, Key: proto.KeyShiftSpace})
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "a"}); snap.Commit != "ａ" || !snap.FullWidth || snap.Mode != proto.ModeEnglish {
		t.Fatalf("wide session: %+v", snap)
	}
	if snap := styleDo(t, e, b, proto.Request{Type: proto.Key, Key: "a"}); snap.Handled || snap.FullWidth || snap.Mode != proto.ModeEnglish {
		t.Fatalf("width leaked through shared language mode: %+v", snap)
	}
	styleDo(t, e, a, proto.Request{Type: proto.Cancel})
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "1"}); snap.Commit != "１" || !snap.FullWidth || snap.Mode != proto.ModeEnglish {
		t.Fatalf("reset lost width or mode: %+v", snap)
	}
	styleDo(t, e, a, proto.Request{Type: proto.Toggle})
	styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "t"})
	styleDo(t, e, a, proto.Request{Type: proto.Key, Key: " "})
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Commit}); snap.Commit != "吃" || snap.Mode != proto.ModeZhuyin || !snap.FullWidth {
		t.Fatalf("focus commit changed mode or width: %+v", snap)
	}
	e.CloseSession(a)
	c := e.OpenSession()
	if snap := styleDo(t, e, c, proto.Request{Type: proto.Display, Columns: 1}); snap.FullWidth {
		t.Fatalf("new session inherited old width: %+v", snap)
	}
}

func TestStylesAreCopiedAndCapsRemainLocal(t *testing.T) {
	e := testEngine()
	p, _ := inputstyle.Get("mcbopomofo")
	e.InputStyle = p.Rules
	e.CapsLockEnglish = true
	e.SharedMode = true
	a := e.OpenSession()
	zero, _ := inputstyle.Get("0c")
	e.InputStyle = zero.Rules
	b := e.OpenSession()
	styleDo(t, e, a, proto.Request{Type: proto.CapsLock, On: true})
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "a"}); snap.Commit != "a" || !snap.CapsEnglish || snap.Mode != proto.ModeEnglish {
		t.Fatalf("McBopomofo lowercase route: %+v", snap)
	}
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "A"}); !snap.Handled || snap.Commit != "A" {
		t.Fatalf("shell-normalized Caps+Shift must commit uppercase: %+v", snap)
	}
	if snap := styleDo(t, e, b, proto.Request{Type: proto.Display, Columns: 1}); snap.Mode != proto.ModeZhuyin || snap.CapsLock {
		t.Fatalf("temporary caps leaked: %+v", snap)
	}
	styleDo(t, e, b, proto.Request{Type: proto.CapsLock, On: true})
	if snap := styleDo(t, e, b, proto.Request{Type: proto.Key, Key: "a"}); snap.Handled {
		t.Fatalf("new 0c session inherited lowercase profile: %+v", snap)
	}
	if snap := styleDo(t, e, a, proto.Request{Type: proto.CapsLock, On: false}); snap.Mode != proto.ModeZhuyin || snap.CapsEnglish {
		t.Fatalf("temporary mode did not restore: %+v", snap)
	}
}

func TestStylePrefixCannotCrossFocusOrMode(t *testing.T) {
	e := testEngine()
	p, _ := inputstyle.Get("microsoft")
	e.InputStyle = p.Rules
	a, b := e.OpenSession(), e.OpenSession()
	styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "`"})
	if _, n := e.Sessions(); n != 1 {
		t.Fatalf("prefix must count as composition, got %d", n)
	}
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Commit}); snap.Commit != "`" || snap.Text() != "" {
		t.Fatalf("focus commits displayed prefix exactly once: %+v", snap)
	}
	if snap := styleDo(t, e, a, proto.Request{Type: proto.Commit}); snap.Commit != "" {
		t.Fatalf("duplicate focus commit: %+v", snap)
	}
	styleDo(t, e, b, proto.Request{Type: proto.Mode, On: true})
	if snap := styleDo(t, e, b, proto.Request{Type: proto.Key, Key: ","}); snap.Handled {
		t.Fatalf("prefix leaked to next field: %+v", snap)
	}
	for _, op := range []proto.Request{{Type: proto.Key, Key: proto.KeyEscape}, {Type: proto.Cancel}, {Type: proto.Mode, On: true}} {
		styleDo(t, e, a, proto.Request{Type: proto.Mode, On: false})
		styleDo(t, e, a, proto.Request{Type: proto.Key, Key: "`"})
		if snap := styleDo(t, e, a, op); snap.Text() != "" || snap.Commit != "" {
			t.Fatalf("prefix cancellation %+v: %+v", op, snap)
		}
		if _, n := e.Sessions(); n != 0 {
			t.Fatalf("stale prefix after %+v", op)
		}
	}
}

func TestStyleHorizontalCandidateColumnsSurviveDisplayNegotiation(t *testing.T) {
	for _, size := range []int{9, 10} {
		e := testEngine()
		p, _ := inputstyle.Get("chewing")
		p.Rules.CandidatePageSize = size
		e.InputStyle = p.Rules
		e.CandidateColumns = 10
		id := e.OpenSession()
		styleDo(t, e, id, proto.Request{Type: proto.Display, Columns: 10})
		styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "t"})
		styleDo(t, e, id, proto.Request{Type: proto.Key, Key: " "})
		snap := styleDo(t, e, id, proto.Request{Type: proto.Candidates})
		if snap.Candidates == nil || snap.Candidates.Columns != size {
			t.Fatalf("horizontal page %d: %+v", size, snap)
		}
		// UI reports the available width, including a restored horizontal row.
		styleDo(t, e, id, proto.Request{Type: proto.Display, Columns: 5})
		snap = styleDo(t, e, id, proto.Request{Type: proto.Display, Columns: 10})
		if snap.Candidates.Columns != size {
			t.Fatalf("display still clamped to grid: %+v", snap)
		}
		snap = styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeyRight})
		if snap.Candidates.Highlight != 1 {
			t.Fatalf("horizontal navigation changed: %+v", snap)
		}
	}
}
