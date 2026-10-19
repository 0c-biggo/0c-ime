package session

import (
	"bytes"
	"testing"

	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
)

const englishDay = int64(86400)

func TestMixedEnglishLearnsOnlyCommittedIndependentWords(t *testing.T) {
	s := mixedSession()
	m := s.model
	for i := 0; i < 20; i++ {
		mixedType(s, "newWidget_52")
		s.Handle(proto.Request{Type: proto.Candidates})
		s.Handle(proto.Request{Type: proto.Close})
		s.snapshot()
	}
	if len(m.Store.English) != 0 {
		t.Fatal("typing/redraw learned before commit")
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	for day := 100; day < 102; day++ {
		m.AdvanceTime(int64(day) * englishDay)
		for j := 0; j < 2; j++ {
			mixedType(s, "newWidget_52 newWidget_52")
			key(s, proto.KeyEnter)
		}
	}
	if !m.EnglishKnown("newWidget_52") || m.Store.English["newwidget_52"].DayConfirmed != 2 {
		t.Fatalf("independent input did not learn: %+v", m.Store.English)
	}
	if m.Store.Uni["newWidget_52"] != 0 {
		t.Fatal("English leaked into Chinese store")
	}
	mixedType(s, "newW")
	if s.snapshot().SuggestionEdit == nil || s.snapshot().SuggestionEdit.Text != "newWidget_52" {
		t.Fatalf("confirmed identifier not suggested: %+v", s.snapshot())
	}
	key(s, proto.KeyTab)
	key(s, " ")
	key(s, proto.KeyEnter)
	e := m.Store.English["newwidget_52"]
	if e.DayConfirmed != 2 {
		t.Fatalf("assistance independently confirmed: %+v", e)
	}
	var b bytes.Buffer
	m.Store.Save(&b)
	st, err := personal.LoadStore(&b)
	if err != nil {
		t.Fatal(err)
	}
	m.Store = st
	mixedType(s, "newW")
	if s.snapshot().SuggestionEdit == nil || s.snapshot().SuggestionEdit.Text != "newWidget_52" {
		t.Fatal("restart lost confirmed suggestion")
	}
}

func TestMixedEnglishAssistedOriginsSurviveCursorEdits(t *testing.T) {
	for _, edit := range []string{"unchanged", "cursor", "delete", "undo"} {
		t.Run(edit, func(t *testing.T) {
			s := mixedSession()
			s.model.AdvanceTime(100 * englishDay)
			mixedType(s, "recieve")
			key(s, proto.KeyTab)
			switch edit {
			case "cursor":
				s.Handle(proto.Request{Type: proto.Cursor, To: 0})
				mixedType(s, "ji3")
				s.Handle(proto.Request{Type: proto.Cursor, To: len(s.syls)})
			case "delete":
				key(s, " ")
				key(s, proto.KeyBackspace)
				key(s, proto.KeyBackspace)
			case "undo":
				key(s, proto.KeyEscape)
			}
			key(s, proto.KeyEnter)
			if edit == "undo" {
				e := s.model.Store.English["recieve"]
				if e.Confirmed != 1 || !e.Suspicious {
					t.Fatalf("raw undo should count with typo barrier: %+v", e)
				}
			} else if len(s.model.Store.English) != 0 {
				t.Fatalf("edited assisted word confirmed: %+v", s.model.Store.English)
			}
		})
	}
}

func TestMixedEnglishCancelPrivacyPauseAndURLsLeaveNoEvidence(t *testing.T) {
	for _, mode := range []string{"cancel", "private", "pause", "resume", "symbol", "url", "path", "off"} {
		t.Run(mode, func(t *testing.T) {
			s := mixedSession()
			text := "newWidget_52"
			switch mode {
			case "private":
				s.Handle(proto.Request{Type: proto.Key, Key: "n", Private: true})
				text = "ewWidget_52"
			case "pause", "resume":
				s.Handle(proto.Request{Type: proto.Pause, On: true})
			case "symbol":
				s.Handle(proto.Request{Type: proto.Symbol, Symbol: text})
				text = ""
			case "url":
				text = "https://example.com/a?q=1"
			case "path":
				text = "C:\\Users\\kevin"
			case "off":
				s.MixedInput = false
			}
			mixedType(s, text)
			if mode == "resume" {
				s.Handle(proto.Request{Type: proto.Pause, On: false})
			}
			if mode == "cancel" {
				s.Handle(proto.Request{Type: proto.Cancel})
			} else {
				key(s, proto.KeyEnter)
			}
			if len(s.model.Store.English) != 0 {
				t.Fatalf("%s learned: %+v", mode, s.model.Store.English)
			}
		})
	}
}

func TestMixedEnglishPrivateQueriesCannotExposePersonalNames(t *testing.T) {
	s := mixedSession()
	for day := 100; day < 103; day++ {
		s.model.AdvanceTime(int64(day) * englishDay)
		mixedType(s, "newWidget_52")
		key(s, proto.KeyEnter)
	}
	if !s.model.EnglishKnown("newWidget_52") {
		t.Fatal("fixture did not promote")
	}
	s.Handle(proto.Request{Type: proto.Key, Key: "n", Private: true})
	mixedType(s, "ewW")
	if snap := s.snapshot(); snap.SuggestionEdit != nil && snap.SuggestionEdit.Text == "newWidget_52" {
		t.Fatal("private field exposed personal identifier")
	}
	s.Handle(proto.Request{Type: proto.Candidates})
	for _, item := range s.snapshot().Candidates.Items {
		if item.Text == "newWidget_52" {
			t.Fatal("private candidates exposed personal identifier")
		}
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	mixedType(s, "newW")
	s.Handle(proto.Request{Type: proto.Candidates})
	if s.snapshot().Candidates == nil {
		t.Fatal("fixture did not open candidates")
	}
	s.Handle(proto.Request{Type: proto.Page, Index: 0, Private: true})
	if s.snapshot().Candidates != nil {
		t.Fatal("open personal candidates crossed into private state")
	}
	s.Handle(proto.Request{Type: proto.Cancel})
	mixedType(s, "newW")
	s.Handle(proto.Request{Type: proto.Candidates})
	s.Handle(proto.Request{Type: proto.Forget, Word: "newWidget_52"})
	for _, item := range s.snapshot().Candidates.Items {
		if item.Text == "newWidget_52" {
			t.Fatal("open candidates retained forgotten personal word")
		}
	}
}

func TestMixedEnglishEditsForgetAndExplicitRecovery(t *testing.T) {
	s := mixedSession()
	m := s.model
	m.AdvanceTime(100 * englishDay)
	mixedType(s, "newWidget_52")
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	mixedType(s, "extra")
	key(s, proto.KeyEnter)
	if _, ok := m.Store.English["newwidget_52"]; ok {
		t.Fatal("replaced spelling kept positive evidence")
	}
	if e := m.Store.English["extranewwidget_52"]; e.Confirmed != 1 {
		t.Fatalf("final edited word missing: %+v", m.Store.English)
	}
	for day := 101; day < 104; day++ {
		m.AdvanceTime(int64(day) * englishDay)
		mixedType(s, "newWidget_52")
		key(s, proto.KeyEnter)
	}
	s.Handle(proto.Request{Type: proto.Forget, Word: "newWidget_52"})
	for day := 104; day < 107; day++ {
		m.AdvanceTime(int64(day) * englishDay)
		mixedType(s, "newWidget_52")
		key(s, proto.KeyEnter)
	}
	if m.EnglishKnown("newWidget_52") || !m.Store.Tombstones["newwidget_52"] {
		t.Fatal("passive input undid forget")
	}
	mixedType(s, "newWidget_52")
	s.Handle(proto.Request{Type: proto.Candidates})
	s.Handle(proto.Request{Type: proto.Select, Index: 0})
	key(s, proto.KeyEnter)
	if m.Store.Tombstones["newwidget_52"] || m.EnglishKnown("newWidget_52") {
		t.Fatal("explicit selection must restart, not skip, confirmation")
	}
	m.AdvanceTime(107 * englishDay)
	mixedType(s, "newWidget_52")
	key(s, proto.KeyEnter)
	mixedType(s, "newWidget_52")
	key(s, proto.KeyEnter)
	if !m.EnglishKnown("newWidget_52") {
		t.Fatal("explicit recovery never promoted")
	}
}
