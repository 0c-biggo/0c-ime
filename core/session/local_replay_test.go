package session

import (
	"reflect"
	"testing"

	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/zhuyin"
)

func TestLocalEvidenceRebuiltFromRequestsAndCompletionIsNotManual(t *testing.T) {
	s := evidenceSession()
	var requests []proto.Request
	do := func(r proto.Request) proto.Snapshot { requests = append(requests, r); return s.Handle(r) }
	for _, y := range zhuyin.ParseReading("ㄒㄧㄣ ㄊㄨˊ ㄅㄧㄥˋ ㄒㄧㄣ ㄊㄨˊ") {
		for _, k := range layout.Standard.Keys(y) {
			do(proto.Request{Type: proto.Key, Key: string(k)})
		}
	}
	do(proto.Request{Type: proto.Cursor, To: 0})
	c := do(proto.Request{Type: proto.Candidates, Length: 1}).Candidates
	for _, v := range c.Items {
		if v.Text == "新" {
			do(proto.Request{Type: proto.Select, Index: v.ID})
			break
		}
	}
	want := s.snapshot()
	for range 2 {
		reconnected := evidenceSession()
		for _, r := range requests {
			reconnected.Handle(r)
		}
		if !reflect.DeepEqual(want, reconnected.snapshot()) || reconnected.model.Store.Total != 0 {
			t.Fatal("重播須重建相同暫時證據，不累加永久學習")
		}
	}
	lock := s.locks[0]
	lock.Completion = true
	s.locks[0] = lock
	s.refresh()
	if got := s.snapshot().Text(); got != "新圖並心圖" {
		t.Fatal("Tab 來源不得當成手選擴散", got)
	}
}
