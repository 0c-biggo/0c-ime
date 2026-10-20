package session

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestCurrentCorrectionOverridesPersistedPreference(t *testing.T) {
	s := evidenceSession()
	s.model.Learn(personal.Commit{Syls: zhuyin.ParseReading("ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ"), Path: []personal.Seg{{Word: "並", End: 1}, {Word: "心", Start: 1, End: 2, Manual: true}, {Word: "圖", Start: 2, End: 3}}})
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ")
	selectText(t, s, 0, "新")
	if got := s.snapshot().Text(); got != "新圖並新圖" {
		t.Fatal("當次更正應優先於舊的條件偏好", got)
	}
	key(s, proto.KeyEscape)
	if got := typeReadingLocal(s, "ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ").Text(); got != "並心圖" {
		t.Fatal("取消不得替換舊的持久偏好", got)
	}
}

func TestScopedCorrectionCommitRestartAndCandidateRanking(t *testing.T) {
	s := evidenceSession()
	typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ")
	selectText(t, s, 0, "新")
	if len(s.model.Store.Corrections) != 0 {
		t.Fatal("選取即持久化")
	}
	key(s, proto.KeyEnter)
	if len(s.model.Store.Corrections) != 1 {
		t.Fatal("提交必須恰好學習一次")
	}
	var b bytes.Buffer
	if err := s.model.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	restored, err := personal.LoadStore(&b)
	if err != nil {
		t.Fatal(err)
	}
	old := s.model.Store
	s.model.Store = restored
	if !reflect.DeepEqual(old, restored) {
		t.Fatal("存檔不一致")
	}
	s = New(s.model)
	s.Opt.PrefixAware = false
	if got := typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ").Text(); got != "新圖" {
		t.Fatal("重啟後遺失更正", got)
	}
	s.Handle(proto.Request{Type: proto.Cursor, To: 0})
	snap := s.Handle(proto.Request{Type: proto.Candidates, Length: 1})
	if snap.Candidates.Items[0].Text != "新" {
		t.Fatal("候選排序未使用相同條件偏好", snap.Candidates)
	}
}

func TestCorrectionDoesNotPersistCancelledOrPrivateSelection(t *testing.T) {
	for _, mode := range []string{"cancel", "delete", "private", "pause"} {
		t.Run(mode, func(t *testing.T) {
			s := evidenceSession()
			typeReadingLocal(s, "ㄒㄧㄣ ㄊㄨˊ")
			selectText(t, s, 0, "新")
			switch mode {
			case "cancel":
				key(s, proto.KeyEscape)
			case "delete":
				s.Handle(proto.Request{Type: proto.Cursor, To: 1})
				key(s, proto.KeyBackspace)
				key(s, proto.KeyEnter)
			case "private":
				s.Handle(proto.Request{Type: proto.Commit, Private: true})
			case "pause":
				s.Handle(proto.Request{Type: proto.Pause, On: true})
				key(s, proto.KeyEnter)
			}
			if len(s.model.Store.Corrections) != 0 {
				t.Fatal("不應留下持久更正")
			}
		})
	}
}
