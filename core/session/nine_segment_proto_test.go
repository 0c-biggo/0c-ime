package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 快照：nine_segments 依序列出每個九宮格音節的位置、第幾顆鍵起、佔幾顆、讀音（不是空字串；還沒打完時是開頭拼法並標
// partial）、錨點與固定；左欄另帶第一顆鍵的位置與每一項的鍵數。沒開自動切字時沒有 nine_segments，左欄也沒有新欄位。
func TestNineSegmentSnapshot(t *testing.T) {
	s := segSession()
	press(s, 7, 2, 0)
	s.Handle(proto.Request{Type: proto.Symbol, Group: "ㄋ"}) // 長按選的單一聲母：一定還沒打完
	snap := s.View()
	want := []proto.NineSegment{
		{At: 0, Key: 0, Keys: 2, Reading: s.syls[0].Text(), Anchor: true},
		{At: 1, Key: 2, Keys: 1, Reading: "ㄋ", Partial: true},
	}
	if !reflect.DeepEqual(snap.NineSegments, want) {
		t.Fatalf("segments %+v", snap.NineSegments)
	}
	if r := snap.NineReadings; r == nil || r.At != 0 || r.Key != 0 || len(r.Keys) != len(r.Readings) {
		t.Fatalf("side column %+v", r)
	}
	data, _ := json.Marshal(snap)
	for _, field := range []string{`"nine_segments":[{"at":0,"key":0,"keys":2,`, `"anchor":true,"fixed":false}`, `"partial":true`, `"keys":[`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("json lacks %s: %s", field, data)
		}
	}
	snap = s.Handle(proto.Request{Type: proto.NineReading, To: 0, Word: s.syls[0].Text(), Length: 2})
	if g := snap.NineSegments[0]; !g.Fixed || !g.Anchor {
		t.Fatalf("fixed segment %+v", g)
	}
	if r := snap.NineReadings; r == nil || r.At != 1 || r.Key != 2 || !r.Pending || r.Readings[0] != "ㄋ" {
		t.Fatalf("side column after fixing %+v", r)
	}
	s = nineSession()
	snap = press(s, 7, 2, 0)
	data, _ = json.Marshal(snap)
	if snap.NineSegments != nil || strings.Contains(string(data), "nine_segments") || strings.Contains(string(data), `"keys"`) || strings.Contains(string(data), `"key"`) {
		t.Fatalf("off: %s", data)
	}
}

// 手機打字時候選列一直開著（外殼每鍵要一次 candidates）：按下一顆鍵時候選關掉、照常重新切，外殼再要一次候選。
func TestNineSegmentKeyWithCandidatesOpen(t *testing.T) {
	s := segSession()
	press(s, keysOf("ㄋㄧ")...)
	if snap := s.Handle(proto.Request{Type: proto.Candidates}); snap.Candidates == nil {
		t.Fatal("no candidates")
	}
	snap := press(s, keysOf("ㄏㄠ")...)
	if snap.Text() != "你好" || segs(s) != "2 2" || snap.Candidates != nil {
		t.Fatalf("got %q %s %+v", snap.Text(), segs(s), snap.Candidates)
	}
	snap = s.Handle(proto.Request{Type: proto.Candidates})
	if snap.Candidates == nil || snap.Candidates.Start+snap.Candidates.Length > 2 {
		t.Fatalf("candidates %+v", snap.Candidates)
	}
}
