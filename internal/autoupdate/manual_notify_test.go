package autoupdate

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// #340：「手動更新」（manual）照常在背景檢查，有新版時跟「下載後詢問」一樣通知（系統通知、選單第一列、更新頁），
// 但不自己下載；通知與選單的動作是「立即下載」，下載好後再按「立即安裝」。提醒間隔沿用 24／72 小時。
func TestManualModeChecksAndNotifiesButNeverDownloads(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}} // 閒置：auto 會直接裝完，manual 不能
	co, clk := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode = ModeManual
	_ = Save(co.Base, s)
	start := clk.now()
	var at []time.Duration
	for i := 0; i < 4*100; i++ { // 每 15 分鐘一次排程，共 100 小時
		before := len(p.notices)
		s = run(t, co)
		if len(p.notices) > before {
			at = append(at, clk.now().Sub(start))
		}
		if s.Stage != StageFound || s.Blocked == nil || s.Blocked.Reason != "needs_download" {
			t.Fatalf("手動更新要停在有新版、等你按立即下載：%s %+v", s.Stage, s.Blocked)
		}
		clk.add(15 * time.Minute)
	}
	for _, c := range p.calls {
		if c != "fetch" {
			t.Fatalf("手動更新只能檢查，不能下載或安裝：%v", p.calls)
		}
	}
	if n := strings.Count(strings.Join(p.calls, " "), "fetch"); n < 10 {
		t.Fatalf("背景要照排程檢查（約每 6 小時），只查了 %d 次", n)
	}
	want := []time.Duration{0, 24 * time.Hour, 72 * time.Hour, 96 * time.Hour}
	if !slices.Equal(at, want) {
		t.Fatalf("通知時間 %v，要 %v", at, want)
	}
	if p.notices[0] != "靈犀注音有新版本" {
		t.Fatalf("通知標題 %q", p.notices[0])
	}
	n := notice(s)
	if n.Action != "download" || n.TargetVersion != "0.1.0-dev.18" || n.TargetSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("通知要附「立即下載」並綁定這一版：%+v", n)
	}
	if !strings.Contains(n.Body, "立即下載") || !strings.Contains(n.Body, "0.1.0-dev.18") {
		t.Fatalf("通知內文：%q", n.Body)
	}

	// 按「立即下載」：只下載與核對，不安裝；之後等「立即安裝」。
	p.calls = nil
	if s, err := co.DownloadNow(context.Background(), "0.1.0-dev.18", strings.Repeat("a", 64)); err != nil ||
		s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "needs_consent" {
		t.Fatalf("立即下載後要等你安裝：%v %+v", err, s)
	}
	if got := strings.Join(p.calls, " "); got != "fetch download prepare" {
		t.Fatalf("立即下載只下載與準備：%s", got)
	}
	n = notice(mustLoad(t, co))
	if n.Action != "finish" {
		t.Fatalf("下載好之後的通知是「立即安裝」：%+v", n)
	}
	// download-now -notify 當場送了這則（NoticeDelivered）：背景提醒不在下一次排程重複。
	if err := NoticeDelivered(co.Base, n, clk.now()); err != nil {
		t.Fatal(err)
	}
	before := len(p.notices)
	for i := 0; i < 8; i++ { // 背景排程不會自己裝
		clk.add(15 * time.Minute)
		if s = run(t, co); s.Stage != StagePrepared || s.Blocked == nil || s.Blocked.Reason != "needs_consent" {
			t.Fatalf("手動更新下載好後要等你安裝：%s %+v", s.Stage, s.Blocked)
		}
	}
	if len(p.notices) != before {
		t.Fatalf("剛送過「立即安裝」的通知，兩小時內不再提醒：%v", p.notices[before:])
	}
	if slices.Contains(p.calls, "switch") {
		t.Fatalf("沒按立即安裝就裝了：%v", p.calls)
	}
	// 按「立即安裝」才裝。
	co.Consent = true
	if s = run(t, co); s.Stage != StageActive || s.Mode != ModeManual {
		t.Fatalf("按立即安裝要完成：%s %+v", s.Stage, s.Blocked)
	}
}

// 暫停更新 7 天照舊：暫停中不檢查、不通知。
func TestManualModePausedDoesNotCheck(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
	co, clk := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode, s.PausedUntil = ModeManual, clk.now().Add(7*24*time.Hour)
	_ = Save(co.Base, s)
	for i := 0; i < 4*24; i++ {
		run(t, co)
		clk.add(15 * time.Minute)
	}
	if len(p.calls) != 0 || len(p.notices) != 0 {
		t.Fatalf("暫停中不能檢查或通知：%v %v", p.calls, p.notices)
	}
	clk.add(7 * 24 * time.Hour)
	if s = run(t, co); s.Stage != StageFound || s.Blocked == nil || s.Blocked.Reason != "needs_download" || len(p.notices) != 1 {
		t.Fatalf("暫停結束後照常檢查與通知：%s %+v %v", s.Stage, s.Blocked, p.notices)
	}
}

// 選單的「檢查更新」（auto -now）在手動更新時只檢查，不下載；設定程式的「檢查更新」（Check）查到新版也標成可下載，
// 選單第一列就能顯示「立即下載」。
func TestManualModeCheckButtonsDoNotDownload(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: time.Hour, IdleKnown: true}}
	co, _ := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode = ModeManual
	_ = Save(co.Base, s)
	co.Force = true
	if s = run(t, co); s.Stage != StageFound || s.Blocked == nil || s.Blocked.Reason != "needs_download" || strings.Join(p.calls, " ") != "fetch" {
		t.Fatalf("選單的檢查更新只檢查：%s %+v %v", s.Stage, s.Blocked, p.calls)
	}

	p2 := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
	co2, _ := newCoordinator(t, p2)
	s, _ = Load(co2.Base)
	s.Mode = ModeManual
	_ = Save(co2.Base, s)
	s, err := co2.Check(context.Background())
	if err != nil || s.Stage != StageFound || s.Blocked == nil || s.Blocked.Reason != "needs_download" || strings.Join(p2.calls, " ") != "fetch" {
		t.Fatalf("設定程式的檢查更新：%v %s %+v %v", err, s.Stage, s.Blocked, p2.calls)
	}
	// 下載後詢問、自動：Check 不加這個待辦（背景自己會下載）。
	p3 := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
	co3, _ := newCoordinator(t, p3)
	if s, err = co3.Check(context.Background()); err != nil || s.Stage != StageFound || s.Blocked != nil {
		t.Fatalf("自動模式的檢查更新：%v %s %+v", err, s.Stage, s.Blocked)
	}
}

// 從手動更新改回自動或下載後詢問：背景照常下載，「立即下載」的待辦跟著消失。
func TestLeavingManualModeDownloads(t *testing.T) {
	for _, mode := range []string{ModeAuto, ModeAsk} {
		t.Run(mode, func(t *testing.T) {
			p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
				cond: Conditions{Idle: time.Hour, IdleKnown: true}}
			co, clk := newCoordinator(t, p)
			s, _ := Load(co.Base)
			s.Mode = ModeManual
			_ = Save(co.Base, s)
			run(t, co)
			if _, err := EditState(co.Base, func(s *State) error { s.Mode = mode; return nil }); err != nil {
				t.Fatal(err)
			}
			clk.add(15 * time.Minute)
			s = run(t, co)
			want := map[string]string{ModeAuto: StageActive, ModeAsk: StagePrepared}[mode]
			if s.Stage != want || (s.Blocked != nil && s.Blocked.Reason == "needs_download") {
				t.Fatalf("改成 %s 後：%s %+v", mode, s.Stage, s.Blocked)
			}
		})
	}
}

// 通知的「稍後 1 小時」在還沒下載時也有效：同一版一小時內不再提醒。
func TestManualDownloadNoticeCanBeSnoozed(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
	co, clk := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Mode = ModeManual
	_ = Save(co.Base, s)
	run(t, co)
	if len(p.notices) != 1 {
		t.Fatalf("第一次查到就通知：%v", p.notices)
	}
	// 隔一天第二則提醒時按「稍後 1 小時」。
	clk.add(24 * time.Hour)
	run(t, co)
	if len(p.notices) != 2 {
		t.Fatalf("24 小時後第二則：%v", p.notices)
	}
	if err := SnoozeNotice(co.Base, "0.1.0-dev.18", strings.Repeat("a", 64), clk.now()); err != nil {
		t.Fatalf("還沒下載的新版也能稍後提醒：%v", err)
	}
	clk.add(30 * time.Minute)
	run(t, co)
	if len(p.notices) != 2 {
		t.Fatalf("稍後期間不提醒：%v", p.notices)
	}
	clk.add(31 * time.Minute)
	run(t, co)
	if len(p.notices) != 3 || p.actions[2] != "download" {
		t.Fatalf("一小時後再提醒一次，仍是立即下載：%v %v", p.notices, p.actions)
	}
}
