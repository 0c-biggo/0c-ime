package engine

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #297：關閉「記住我選的字詞」由引擎帶給之後開的每個 session：照常送出，只是不寫進共用的個人資料。
func TestNoLearningReachesSessions(t *testing.T) {
	for _, off := range []bool{false, true} {
		e := testEngine()
		e.NoLearning = off
		id := e.OpenSession()
		styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "t"})
		styleDo(t, e, id, proto.Request{Type: proto.Key, Key: " "}) // 吃
		if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap.Commit != "吃" {
			t.Fatalf("關閉 %v：送出 %q", off, snap.Commit)
		}
		if learned := e.Model.Store.Total != 0; learned == off {
			t.Fatalf("關閉 %v：個人資料 Total=%v", off, e.Model.Store.Total)
		}
	}
}
