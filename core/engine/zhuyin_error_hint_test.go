package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// #300：設定「打錯注音時提示」由引擎帶給之後開的每個 session；開著時 ㄏㄟˊ（沒有字）擋下聲調、快照帶 bell，
// 緊接著再按一次 ˊ 照舊原樣留下。關閉時照舊原樣留下（#109）。
func TestZhuyinErrorHintReachesSessions(t *testing.T) {
	for _, on := range []bool{false, true} {
		e := testEngine()
		e.ZhuyinErrorHint = on
		id := e.OpenSession()
		var snap proto.Snapshot
		for _, k := range []string{"c", "o", "6"} {
			snap = styleDo(t, e, id, proto.Request{Type: proto.Key, Key: k})
		}
		want := map[bool]string{false: "ㄏㄟˊ", true: "ㄏㄟ"}[on]
		if snap.Text() != want || snap.Bell != on {
			t.Fatalf("開關 %v：組字 %q bell %v，要 %q", on, snap.Text(), snap.Bell, want)
		}
		raw, err := e.Handle(id, []byte(`{"type":"key","key":"6"}`))
		if err != nil {
			t.Fatal(err)
		}
		var back proto.Snapshot
		if err := json.Unmarshal(raw, &back); err != nil || back.Text() != "ㄏㄟˊ" && on || strings.Contains(string(raw), `"bell"`) {
			t.Fatalf("開關 %v：再按一次 ˊ 原樣留下、不再提示：%s", on, raw)
		}
	}
}
