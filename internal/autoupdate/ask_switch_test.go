package autoupdate

import (
	"testing"
	"time"
)

// 設定程式「更新方式」選「下載後詢問」（ask，#340）：已經下載好、等閒置的那一版改成等你按「立即安裝」，並且照樣通知有新版本；
// 提醒沿用 24／72 小時的間隔，不會每 15 分鐘的排程都跳一次。改回自動更新（auto）就照舊在閒置時完成，不必再按。
func TestTurningAutoUpdateOffStillNotifiesNewVersion(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: 0, IdleKnown: true}} // 使用者正在用電腦：auto 會等閒置
	co, clk := newCoordinator(t, p)
	start := clk.now()
	s := run(t, co)
	if s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "waiting_idle" || len(p.notices) != 0 {
		t.Fatalf("auto 先準備好、等閒置，不通知：%s %+v %v", s.Stage, s.Blocked, p.notices)
	}

	clk.add(time.Hour)
	if _, err := EditState(co.Base, func(s *State) error { s.Mode = ModeAsk; return nil }); err != nil {
		t.Fatal(err)
	}
	var at []time.Duration
	for i := 0; i < 4*100; i++ { // 每 15 分鐘一次排程，共 100 小時
		before := len(p.notices)
		s = run(t, co)
		if len(p.notices) > before {
			at = append(at, clk.now().Sub(start))
		}
		if s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "needs_consent" {
			t.Fatalf("關掉自動更新後要停在等你安裝：%s %+v", s.Stage, s.Blocked)
		}
		clk.add(15 * time.Minute)
	}
	for _, c := range p.calls {
		if c == "switch" {
			t.Fatal("關掉自動更新後不能自己安裝")
		}
	}
	want := []time.Duration{time.Hour, 25 * time.Hour, 72 * time.Hour, 96 * time.Hour}
	if len(at) != len(want) {
		t.Fatalf("通知時間 %v，要 %v", at, want)
	}
	for i := range want {
		if at[i] != want[i] {
			t.Fatalf("通知時間 %v，要 %v", at, want)
		}
	}
	if p.notices[0] != "靈犀注音有新版本" {
		t.Fatalf("通知標題 %q", p.notices[0])
	}
	if n := notice(s); n.Action != "finish" || n.TargetVersion != "0.1.0-dev.18" {
		t.Fatalf("通知要附「立即安裝」並綁定這一版：%+v", n)
	}

	// 再打開：閒置時照舊完成，不必按。
	if _, err := EditState(co.Base, func(s *State) error { s.Mode = ModeAuto; return nil }); err != nil {
		t.Fatal(err)
	}
	p.cond.Idle = time.Hour
	if s = run(t, co); s.Stage != StageActive {
		t.Fatalf("打開自動更新後閒置時要完成：%s %+v", s.Stage, s.Blocked)
	}
}
