package session_test

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/0c-biggo/0c-ime/core/layout"
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/session"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 小鶴雙拼打聲調時，每打完一個音節的快照都跟用注音打同一個讀音完全相同，送出的文字也相同：
// 雙拼只是另一種按鍵，選字全交給同一個引擎（同 TestPinyinMatchesZhuyin）。
func TestXiaoheMatchesZhuyin(t *testing.T) {
	m, words := goldenSessionModel()
	rng := rand.New(rand.NewSource(2026))
	zs, xs := session.New(m), session.New(m)
	xs.SetLayout(layout.Xiaohe)
	compared := 0
	for range 200 {
		var reading []zhuyin.Syllable
		for range 1 + rng.Intn(3) {
			reading = append(reading, words[rng.Intn(len(words))].Reading...)
		}
		typable := true
		for _, y := range reading {
			k := layout.Xiaohe.Keys(y)
			if len(k) < 2 {
				typable = false
				break
			}
			pair, _ := layout.Xiaohe.Pair(k[:2])
			b, _ := zhuyin.PinyinSyllable(pair[0])
			typable = typable && b == y.Base() // 跳過 lo、lüan 這種兩鍵預設是別的音節的讀音
		}
		if !typable {
			continue
		}
		for _, y := range reading {
			var zsnap, xsnap proto.Snapshot
			for _, k := range layout.Standard.Keys(y) {
				zsnap = zs.Handle(proto.Request{Type: proto.Key, Key: string(k)})
			}
			for _, k := range layout.Xiaohe.Keys(y) {
				xsnap = xs.Handle(proto.Request{Type: proto.Key, Key: string(k)})
			}
			zsnap.Revision, xsnap.Revision = 0, 0
			if !reflect.DeepEqual(zsnap, xsnap) {
				t.Fatalf("%v 打到 %s：注音 %+v\n雙拼 %+v", reading, y, zsnap, xsnap)
			}
			compared++
		}
		z := zs.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter}).Commit
		x := xs.Handle(proto.Request{Type: proto.Key, Key: proto.KeyEnter}).Commit
		if z != x || z == "" {
			t.Fatalf("%v 送出：注音 %q、雙拼 %q", reading, z, x)
		}
	}
	if compared < 300 {
		t.Fatalf("只比較了 %d 個音節", compared)
	}
}
