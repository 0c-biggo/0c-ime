package personal

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"git.biggo.com/0c/ime/core/english"
	"git.biggo.com/0c/ime/core/lexicon"
	"git.biggo.com/0c/ime/core/lm"
)

func englishModel() *Model { return NewModel(lexicon.NewMem(), lm.NewMem()) }

func TestEnglishIndependentConfirmationsDailyCapAndDecay(t *testing.T) {
	m := englishModel()
	event := EnglishCommit{Text: "LingxiCorn"}
	m.AdvanceTime(100 * daySeconds)
	for i := 0; i < 100; i++ {
		m.LearnEnglish([]EnglishCommit{event, event})
	}
	if e := m.Store.English["lingxicorn"]; e.Confirmed != 2 || e.Days != 1 || m.EnglishKnown(event.Text) {
		t.Fatalf("daily repeats promoted: %+v", e)
	}
	m.AdvanceTime(101 * daySeconds)
	m.LearnEnglish([]EnglishCommit{event})
	if !m.EnglishKnown("lingxicorn") {
		t.Fatal("three commits across two days must promote")
	}
	base := english.New(nil)
	if cs := m.EnglishPrefix(base, "Lingxi", 3); len(cs) != 1 || cs[0].Text != "LingxiCorn" {
		t.Fatalf("confirmed name missing: %+v", cs)
	}
	if cs := m.EnglishCorrections(base, "lingxicorm", 3); len(cs) != 1 || cs[0].Text != "LingxiCorn" {
		t.Fatalf("personal spelling missing: %+v", cs)
	}
	m.AdvanceTime(301 * daySeconds)
	if m.EnglishKnown(event.Text) || len(m.EnglishPrefix(base, "Lingxi", 3)) != 0 {
		t.Fatal("stale support should lose recommendation")
	}
	if _, ok := m.Store.English["lingxicorn"]; !ok {
		t.Fatal("decay must retain observation")
	}
}

func TestEnglishLikelyTypoNeedsMoreIndependentDays(t *testing.T) {
	m := englishModel()
	e := EnglishCommit{Text: "recieve", Suspicious: true}
	for day := 100; day < 103; day++ {
		m.AdvanceTime(int64(day) * daySeconds)
		for i := 0; i < 20; i++ {
			m.LearnEnglish([]EnglishCommit{e})
		}
		if m.EnglishKnown(e.Text) {
			t.Fatal("near-spelling promoted too soon")
		}
	}
	m.AdvanceTime(103 * daySeconds)
	m.LearnEnglish([]EnglishCommit{e})
	m.LearnEnglish([]EnglishCommit{e})
	if !m.EnglishKnown(e.Text) {
		t.Fatal("genuine near-spelling cannot ever be learned")
	}
	confirmed := m.Store.English[e.Text].Confirmed
	for i := 0; i < 100; i++ {
		m.LearnEnglish([]EnglishCommit{{Text: e.Text, Assisted: true}})
	}
	if m.Store.English[e.Text].Confirmed != confirmed {
		t.Fatal("system suggestion confirmed itself")
	}
}

func TestEnglishAssistanceForgetAndIdentifierProtection(t *testing.T) {
	m := englishModel()
	base := english.Prototype()
	for day := 100; day < 110; day++ {
		m.AdvanceTime(int64(day) * daySeconds)
		m.LearnEnglish([]EnglishCommit{{Text: "newWidget", Assisted: true}})
	}
	if len(m.Store.English) != 0 {
		t.Fatal("suggestion created independent observation")
	}
	for _, text := range []string{"newWidget_52", "https://example.com", "C:\\Users\\kevin", "52", "Kelvin", "foo.bar", "中文"} {
		m.LearnEnglish([]EnglishCommit{{Text: text}})
	}
	if len(m.Store.English) != 1 {
		t.Fatalf("URLs/paths/numbers accepted: %+v", m.Store.English)
	}
	m.AdvanceTime(111 * daySeconds)
	m.LearnEnglish([]EnglishCommit{{Text: "newWidget_52"}})
	m.AdvanceTime(112 * daySeconds)
	m.LearnEnglish([]EnglishCommit{{Text: "newWidget_52"}})
	if !m.EnglishKnown("newWidget_52") || len(m.EnglishPrefix(base, "newW", 2)) != 1 {
		t.Fatal("identifier not learned")
	}
	m.Forget("NewWidget_52")
	for day := 120; day < 140; day++ {
		m.AdvanceTime(int64(day) * daySeconds)
		m.LearnEnglish([]EnglishCommit{{Text: "newWidget_52"}, {Text: "newWidget_52", Assisted: true}})
	}
	if m.EnglishKnown("newWidget_52") || len(m.Store.English) != 0 {
		t.Fatal("forgotten word silently relearned")
	}
	m.LearnEnglish([]EnglishCommit{{Text: "newWidget_52", Explicit: true}})
	m.AdvanceTime(140 * daySeconds)
	m.LearnEnglish([]EnglishCommit{{Text: "newWidget_52"}})
	m.LearnEnglish([]EnglishCommit{{Text: "newWidget_52"}})
	if !m.EnglishKnown("newWidget_52") {
		t.Fatal("explicit raw selection did not restart confirmation")
	}
}

func TestEnglishNoSuggestCannotBeBypassedByPersonalization(t *testing.T) {
	m := englishModel()
	base := english.NewFiltered([]english.Word{{Text: "forbidden", Weight: 10}}, []string{"forbidden"})
	for day := 100; day < 103; day++ {
		m.AdvanceTime(int64(day) * daySeconds)
		m.LearnEnglish([]EnglishCommit{{Text: "forbidden"}})
	}
	if !base.Has("forbidden") || base.Suggestible("forbidden") || len(m.EnglishPrefix(base, "forb", 3)) != 0 || len(m.EnglishCorrections(base, "forbiden", 3)) != 0 {
		t.Fatal("no-suggest restriction escaped")
	}
}

func TestEnglishStoreBoundRoundTripAndLegacyMigration(t *testing.T) {
	m := englishModel()
	m.AdvanceTime(100 * daySeconds)
	for i := 0; i < MaxEnglishObservations+20; i++ {
		m.LearnEnglish([]EnglishCommit{{Text: fmt.Sprintf("new_word_%04d", i)}})
	}
	if len(m.Store.English) != MaxEnglishObservations {
		t.Fatal("unbounded observation store")
	}
	var buf bytes.Buffer
	if err := m.Store.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := LoadStore(&buf)
	if err != nil || !reflect.DeepEqual(back, m.Store) {
		t.Fatalf("English roundtrip: %v", err)
	}
	for _, format := range []int{0, 1, 2, 3} {
		s, err := LoadStore(bytes.NewBufferString(fmt.Sprintf(`{"Format":%d,"Uni":{"人工":2},"Pronouns":{},"Total":2}`, format)))
		if err != nil || s.Uni["人工"] != 2 || len(s.English) != 0 {
			t.Fatalf("legacy %d: %v", format, err)
		}
		var migrated bytes.Buffer
		s.Save(&migrated)
		if !bytes.Contains(migrated.Bytes(), []byte(fmt.Sprintf(`"Format":%d`, StoreFormat))) {
			t.Fatal("migration did not bump format")
		}
	}
	_, err = LoadStore(bytes.NewBufferString(fmt.Sprintf(`{"Format":%d,"English":{"newWidget":{"Text":"newWidget"}}}`, StoreFormat+1)))
	var newer *NewerFormatError
	if !errors.As(err, &newer) {
		t.Fatalf("future format loaded: %v", err)
	}
	s, err := LoadStore(bytes.NewBufferString(`{"Format":3,"English":{"invalid/url":{"Text":"invalid/url","Confirmed":10},"valid":{"Text":"valid","Confirmed":100000},"bad":{"Text":"bad","Days":20}}}`))
	if err != nil || len(s.English) != 0 {
		t.Fatalf("invalid evidence retained: %v %+v", err, s)
	}
}
