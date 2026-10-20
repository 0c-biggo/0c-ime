package main

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"git.biggo.com/0c/ime/bridge/ipc"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/internal/autoupdate"
	"git.biggo.com/0c/ime/internal/settings"
)

// liveEngine 回答 open／handle／close／health，記下收到的操作；live 為 false 時像 0.2.1 以前的引擎（沒有即時切換）。
type liveEngine struct {
	mu   sync.Mutex
	live bool
	ops  []string
	on   bool
}

func (f *liveEngine) dial(time.Duration) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		for {
			var env ipc.Envelope
			if ipc.ReadFrame(server, &env) != nil {
				return
			}
			f.mu.Lock()
			f.ops = append(f.ops, env.Op)
			info := map[string]string{"version": "test"}
			if f.live {
				info["output_simplified_live"] = "true"
			}
			reply := ipc.Reply{OK: true, Info: info}
			switch env.Op {
			case ipc.OpOpen:
				reply.Session = 7
			case ipc.OpHandle:
				snap := proto.Snapshot{}
				if env.Req.Type == proto.OutputSimplified && f.live {
					f.on, snap.Handled, snap.OutputSimplified = env.Req.On, true, env.Req.On
					f.ops[len(f.ops)-1] = "switch"
				}
				reply.Snapshot = &snap
			case ipc.OpDrain:
				reply.OK = false // 這個測試不應走到交接
			}
			f.mu.Unlock()
			if ipc.WriteFrame(server, reply) != nil {
				return
			}
		}
	}()
	return client, nil
}

// #169：設定程式（與舊外殼的選單）以 0c-update set output-simplified … -apply 改開關時，引擎能即時切換就不交接重啟：
// 設定照樣先寫檔，再請引擎切換，組字中也立即生效。
func TestSetOutputSimplifiedAppliesLiveWithoutDrain(t *testing.T) {
	base := t.TempDir()
	path := writeSettings(t, base, `{"selection":"statistical"}`)
	engine := &liveEngine{live: true}
	res, err := runSet(base, engine.dial, setOptions{pairs: [][2]string{{"output-simplified", "on"}}, apply: true}, nop)
	if err != nil || res.Apply.State != applyApplied || !res.Apply.Requested {
		t.Fatalf("%+v %v", res, err)
	}
	if s, _ := settings.Load(path); !s.OutputSimplified || s.Selection != "statistical" {
		t.Fatalf("設定檔：%+v", s)
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.on || len(engine.ops) != 3 || engine.ops[0] != ipc.OpOpen || engine.ops[1] != "switch" || engine.ops[2] != ipc.OpClose {
		t.Fatalf("引擎收到的操作：%v（on=%v）", engine.ops, engine.on)
	}
}

// 只有「輸出簡體字」能即時切換；同時改了別的設定，或引擎是舊版（沒有 output_simplified_live）時照舊交接重啟。
func TestOutputSimplifiedLiveOnlyForThatKeyAndNewEngines(t *testing.T) {
	for _, tc := range []struct {
		changes []changeView
		on, ok  bool
	}{
		{[]changeView{{Key: "output-simplified", To: "on"}}, true, true},
		{[]changeView{{Key: "output-simplified", To: "off"}}, false, true},
		{[]changeView{{Key: "output-simplified", To: "on"}, {Key: "punctuation", To: "on"}}, false, false},
		{nil, false, false},
	} {
		if on, ok := liveSimplified(tc.changes); on != tc.on || ok != tc.ok {
			t.Fatalf("%+v：%v %v", tc.changes, on, ok)
		}
	}
	old := &liveEngine{}
	if err := autoupdate.SetOutputSimplified(old.dial, true); !errors.Is(err, autoupdate.ErrNotLive) {
		t.Fatalf("舊引擎：%v", err)
	}
	if old.on || len(old.ops) != 2 || old.ops[1] != ipc.OpClose {
		t.Fatalf("舊引擎不送切換請求：%v", old.ops)
	}
}

// 0c-update status 的 runtime 帶引擎目前的值（外殼選單打勾以它為準）；舊引擎沒有這兩項。
func TestStatusRuntimeReportsLiveSimplified(t *testing.T) {
	got := modelStatus(modelStatusDial(map[string]string{"output_simplified_live": "true", "output_simplified": "true"}, true))
	if got["output_simplified_live"] != true || got["output_simplified"] != true {
		t.Fatalf("%+v", got)
	}
	got = modelStatus(modelStatusDial(map[string]string{"output_simplified": "true"}, true))
	if _, ok := got["output_simplified"]; ok {
		t.Fatalf("舊引擎：%+v", got)
	}
}
