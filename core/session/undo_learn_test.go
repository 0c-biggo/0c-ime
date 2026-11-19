package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 送出後立刻 undo_learn，學到的內容要還原，而且只能還原一次。
func TestUndoLearnRestoresTheLastCommit(t *testing.T) {
	s := newSession()
	before := s.model.Store.Clone()
	for _, r := range "ㄨㄛˇㄒㄧㄤˇ" {
		s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(r)})
	}
	if got := s.Handle(proto.Request{Type: proto.Commit}).Commit; got == "" {
		t.Fatal("nothing sent")
	}
	if s.model.Store.Total == before.Total {
		t.Fatal("the commit learned nothing")
	}
	if !s.Handle(proto.Request{Type: proto.UndoLearn}).Handled {
		t.Fatal("undo_learn not handled")
	}
	if s.model.Store.Total != before.Total || len(s.model.Store.Uni) != len(before.Uni) {
		t.Fatalf("not restored: total %v → %v", before.Total, s.model.Store.Total)
	}
	if s.Handle(proto.Request{Type: proto.UndoLearn}).Handled {
		t.Fatal("restored twice")
	}
}

func TestUnlearnLeavesNoTombstone(t *testing.T) {
	s := newSession()
	for range 3 {
		for _, r := range "ㄨㄛˇㄒㄧㄤˇ" {
			s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(r)})
		}
		s.Handle(proto.Request{Type: proto.Commit})
	}
	if s.model.WordEvidence("想") == 0 {
		t.Fatal("nothing learned")
	}
	s.Handle(proto.Request{Type: proto.Unlearn, Word: "想"})
	if s.model.WordEvidence("想") != 0 || s.model.Store.Tombstones["想"] {
		t.Fatalf("evidence %v tombstone %v", s.model.WordEvidence("想"), s.model.Store.Tombstones["想"])
	}
}

// undo_learn 只復原「上一次」學習；之後的提交沒學到東西、或之後又改了學習資料時，舊的復原複本就作廢，
// 不能把更早那次提交還原，也不能讓已移除的證據回來。
func TestUndoLearnOnlyUndoesTheLatestLearning(t *testing.T) {
	learnOnce := func(t *testing.T, s *Session) {
		for _, r := range "ㄨㄛˇㄒㄧㄤˇ" {
			s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(r)})
		}
		if s.Handle(proto.Request{Type: proto.Commit}).Commit == "" {
			t.Fatal("nothing sent")
		}
	}
	cases := map[string]func(t *testing.T, s *Session){
		"paused commit": func(t *testing.T, s *Session) {
			s.Handle(proto.Request{Type: proto.Pause, On: true})
			learnOnce(t, s)
		},
		"private commit": func(t *testing.T, s *Session) {
			for _, r := range "ㄨㄛˇ" {
				s.Handle(proto.Request{Type: proto.Symbol, Symbol: string(r), Private: true})
			}
			if s.Handle(proto.Request{Type: proto.Commit, Private: true}).Commit == "" {
				t.Fatal("nothing sent")
			}
		},
		"unlearn": func(t *testing.T, s *Session) { s.Handle(proto.Request{Type: proto.Unlearn, Word: "想"}) },
		"forget":  func(t *testing.T, s *Session) { s.Handle(proto.Request{Type: proto.Forget, Word: "想"}) },
	}
	for name, then := range cases {
		t.Run(name, func(t *testing.T) {
			s := newSession()
			learnOnce(t, s)
			then(t, s)
			total, evidence := s.model.Store.Total, s.model.WordEvidence("想")
			if s.Handle(proto.Request{Type: proto.UndoLearn}).Handled {
				t.Error("undo_learn restored an earlier learning")
			}
			if s.model.Store.Total != total || s.model.WordEvidence("想") != evidence {
				t.Fatalf("store changed: total %v → %v, 想 evidence %v → %v",
					total, s.model.Store.Total, evidence, s.model.WordEvidence("想"))
			}
		})
	}
}

// 暫停時點聯想詞不學習，也不能讓 undo_learn 還原前一次提交。
func TestUndoLearnAfterUnlearnedPrediction(t *testing.T) {
	s := predictSession(t)
	commitConfirmed(t, s, "ㄋㄧˇㄏㄠˇ")
	s.Handle(proto.Request{Type: proto.Predict})
	s.paused = true // Pause 請求會清掉前文與聯想詞；直接設定
	if v := s.Handle(proto.Request{Type: proto.Select, Index: 0}); v.Commit != "嗎" {
		t.Fatalf("pick: %+v", v)
	}
	if s.Handle(proto.Request{Type: proto.UndoLearn}).Handled {
		t.Fatal("undo_learn restored the commit before the prediction")
	}
}
