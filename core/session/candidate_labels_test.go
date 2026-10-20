package session

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/0c-biggo/0c-ime/core/decode"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func candidateLabel(t *testing.T, snapshot proto.Snapshot, word string) string {
	t.Helper()
	if snapshot.Candidates != nil {
		for _, item := range snapshot.Candidates.Items {
			if item.Text == word {
				return item.Label
			}
		}
	}
	t.Fatalf("candidate %q missing: %+v", word, snapshot.Candidates)
	return ""
}

func TestCandidateLabelsDoNotFollowRankOrHighlight(t *testing.T) {
	s := newSession()
	s.model.LM.(*lm.Mem).SetUni("景", 1e-7)
	s.model.LM.(*lm.Mem).SetUni("井", 2e-8)
	typeSyl(s, "ㄐㄧㄥˇ")
	snapshot := key(s, proto.KeyDown)
	if snapshot.Candidates.Items[0].Text != "景" {
		t.Fatal("fixture must put the rare word first")
	}
	for _, item := range snapshot.Candidates.Items {
		if item.Label != "" {
			t.Fatalf("rare first candidate or raw Zhuyin mislabeled: %+v", item)
		}
	}
	s = newSession()
	typeSyl(s, "ㄐㄧㄥˇ")
	before := key(s, proto.KeyDown).Candidates.Items
	after := key(s, proto.KeyDown).Candidates.Items
	if !slices.Equal(before, after) || after[0].Label != proto.CandidateCommon {
		t.Fatal("moving the highlight must preserve labels and candidate IDs/order")
	}
	if after[len(after)-1].Label != "" {
		t.Fatal("raw Zhuyin must not receive a label")
	}
}

func TestPersonalLabelRequiresRepeatedEvidenceAndPreservesStore(t *testing.T) {
	s := newSession()
	m := s.model
	m.AdvanceTime(100 * 86400)
	// 舊 Uses／一次手選不代表常用；使用既有真實學習流程累積來源證據。
	m.Store.Uses["井"] = 1000
	learn := func() {
		m.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄐㄧㄥˇ"), Path: []personal.Seg{{Word: "井", End: 1, Manual: true}}})
	}
	learn()
	typeSyl(s, "ㄐㄧㄥˇ")
	if got := candidateLabel(t, key(s, proto.KeyDown), "井"); got == proto.CandidatePersonal {
		t.Fatal("one use or legacy Uses must not imply a habit")
	}
	key(s, proto.KeyEscape)
	for i := 0; i < 11; i++ {
		learn()
	}
	before, _ := json.Marshal(m.Store)
	snapshot := key(s, proto.KeyDown)
	if got := candidateLabel(t, snapshot, "井"); got != proto.CandidatePersonal {
		t.Fatalf("repeated explicit use should be personal, got %q", got)
	}
	after, _ := json.Marshal(m.Store)
	if !bytes.Equal(before, after) || m.Store.Format != personal.StoreFormat && m.Store.Format != 0 {
		t.Fatal("displaying labels changed the personal store")
	}
	snapshot = s.Handle(proto.Request{Type: proto.Pause, On: true})
	if got := candidateLabel(t, snapshot, "井"); got == proto.CandidatePersonal {
		t.Fatal("pausing with the panel open must remove personal labels")
	}
	snapshot = s.Handle(proto.Request{Type: proto.Pause, On: false})
	if got := candidateLabel(t, snapshot, "井"); got != proto.CandidatePersonal {
		t.Fatal("resuming must restore the current personal label")
	}
	snapshot = s.Handle(proto.Request{Type: proto.Key, Key: proto.KeyDown, Private: true})
	if got := candidateLabel(t, snapshot, "井"); got == proto.CandidatePersonal {
		t.Fatal("an open panel must remove personal labels on entering sensitive input")
	}
	key(s, proto.KeyEscape)
	snapshot = s.Handle(proto.Request{Type: proto.Candidates, Length: 1, Private: true})
	if got := candidateLabel(t, snapshot, "井"); got == proto.CandidatePersonal {
		t.Fatal("sensitive input must not reveal a personal usage label")
	}
	key(s, proto.KeyEscape)
	m.AdvanceTime(280 * 86400)
	s.private = false // 新的一般欄位；舊證據已淡出
	if got := candidateLabel(t, key(s, proto.KeyDown), "井"); got == proto.CandidatePersonal {
		t.Fatal("old evidence must decay out of the personal label")
	}
}

func TestRelatedLabelsUseAdjacentContext(t *testing.T) {
	for _, side := range []string{"left", "right"} {
		t.Run(side, func(t *testing.T) {
			s := newSession()
			sys := s.model.LM.(*lm.Mem)
			if side == "left" {
				sys.SetBi("我", "井", .1)
				typeSyl(s, "ㄨㄛˇ")
				typeSyl(s, "ㄐㄧㄥˇ")
			} else {
				sys.SetBi("井", "我", .1)
				typeSyl(s, "ㄐㄧㄥˇ")
				typeSyl(s, "ㄨㄛˇ")
				s.Handle(proto.Request{Type: proto.Cursor, To: 0})
			}
			snapshot := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
			if got := candidateLabel(t, snapshot, "井"); got != proto.CandidateRelated {
				t.Fatalf("adjacent %s context not reflected: %q", side, got)
			}
		})
	}
	s := newSession()
	s.model.LM.(*lm.Mem).SetBi("我", "井", .1)
	typeSyl(s, "ㄨㄛˇ")
	key(s, "<")
	typeSyl(s, "ㄐㄧㄥˇ")
	if got := candidateLabel(t, key(s, proto.KeyDown), "井"); got == proto.CandidateRelated {
		t.Fatal("related label crossed a punctuation boundary")
	}
}

func TestDocumentLabelRequiresConfirmedEnabledContext(t *testing.T) {
	for _, mode := range []string{"confirmed", "unconfirmed", "disabled", "zero-weight", "private", "paused"} {
		t.Run(mode, func(t *testing.T) {
			s := newSession()
			s.model.LM.(*lm.Mem).SetBi("我", "井", .1)
			s.Handle(proto.Request{Type: proto.Context, ContextEnabled: true})
			typeSyl(s, "ㄨㄛˇ")
			commit := key(s, proto.KeyEnter)
			if mode != "unconfirmed" {
				s.Handle(proto.Request{Type: proto.Context, On: true, ContextID: commit.ContextID})
			}
			s.DocumentContextOff = mode == "disabled"
			if mode == "zero-weight" {
				s.Opt.DocumentContextWeight = 0
			}
			typeSyl(s, "ㄐㄧㄥˇ")
			if mode == "paused" {
				s.Handle(proto.Request{Type: proto.Pause, On: true})
			}
			snapshot := s.Handle(proto.Request{Type: proto.Candidates, Length: 1, Private: mode == "private"})
			got := candidateLabel(t, snapshot, "井")
			if (got == proto.CandidateRelated) != (mode == "confirmed") {
				t.Fatalf("%s context got label %q", mode, got)
			}
		})
	}
}

func TestLabelsPreserveNearNotesAndOmitSymbols(t *testing.T) {
	s := newSession()
	entry := lexicon.Entry{Surface: "景", Reading: zhuyin.ParseReading("ㄐㄧㄥˇ"), PRW: 1}
	c := &candState{items: []decode.Candidate{{Entry: entry}}, matched: []string{"ㄐㄧㄥˇ"}}
	c.labels = s.candidateLabels(c)
	s.cand = c
	snapshot := s.snapshot()
	item := snapshot.Candidates.Items[0]
	if item.Note != "近音 ㄐㄧㄥˇ" || item.Label != "" {
		t.Fatalf("near-reading note lost or crowded by a label: %+v", item)
	}
	s.openSymbols([]string{"，", ","}, 0, 0, false)
	for _, item := range s.snapshot().Candidates.Items {
		if item.Label != "" {
			t.Fatal("symbols must not have usage labels")
		}
	}
}
