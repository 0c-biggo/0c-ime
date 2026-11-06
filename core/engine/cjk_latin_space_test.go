package engine

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #273：設定「中英文之間自動加空格」由引擎帶給之後開的每個 session；關閉時送出的文字照舊。
func TestCJKLatinSpaceReachesSessions(t *testing.T) {
	for _, on := range []bool{false, true} {
		e := testEngine()
		e.CJKLatinSpace = on
		id := e.OpenSession()
		styleDo(t, e, id, proto.Request{Type: proto.Key, Key: "t"})
		styleDo(t, e, id, proto.Request{Type: proto.Key, Key: " "}) // 吃
		styleDo(t, e, id, proto.Request{Type: proto.Toggle})
		for _, k := range "OK" {
			styleDo(t, e, id, proto.Request{Type: proto.Key, Key: string(k)})
		}
		want := map[bool]string{false: "吃OK", true: "吃 OK"}[on]
		if snap := styleDo(t, e, id, proto.Request{Type: proto.Key, Key: proto.KeyEnter}); snap.Commit != want {
			t.Fatalf("開關 %v：送出 %q，要 %q", on, snap.Commit, want)
		}
	}
}
