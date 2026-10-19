package autoupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestViewOfDistinguishesTheFourStates(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	target := &Target{Version: "0.1.0-dev.43", Kind: "core", Size: 7}
	for _, c := range []struct {
		name  string
		state State
		want  string
	}{
		{"從沒查過", State{Stage: StageIdle}, CheckNever},
		{"查過且成功、沒有待更新", State{Stage: StageIdle, LastCheck: at, LastSuccess: at}, CheckUpToDate},
		{"離線失敗不能說已是最新", State{Stage: StageIdle, LastCheck: at, CheckFailures: 2, LastError: "offline"}, CheckFailed},
		{"曾成功、最近一次失敗", State{Stage: StageIdle, LastCheck: at, LastSuccess: at.Add(-time.Hour), CheckFailures: 1, LastError: "offline"}, CheckFailed},
		{"有新版", State{Stage: StageFound, Target: target, LastCheck: at, LastSuccess: at}, CheckUpdateAvailable},
		{"已下載準備好", State{Stage: StagePrepared, Target: target, LastCheck: at, LastSuccess: at}, CheckUpdateAvailable},
		{"已知有新版時離線仍如實顯示有新版", State{Stage: StageDownloaded, Target: target, LastCheck: at, CheckFailures: 1, LastError: "offline"}, CheckUpdateAvailable},
		{"已生效", State{Stage: StageActive, LastCheck: at, LastSuccess: at}, CheckUpToDate},
		{"該版本健康檢查失敗、已暫停：不宣稱有新版", State{Stage: StageFailed, Target: target, LastCheck: at, LastSuccess: at}, CheckUpToDate},
	} {
		s := c.state
		if got := ViewOf(&s, "0.1.0-dev.42"); got.State != c.want {
			t.Errorf("%s：得到 %s，應為 %s", c.name, got.State, c.want)
		}
	}
	s := State{Stage: StageFound, Target: target, LastCheck: at, LastSuccess: at, NextCheck: at.Add(6 * time.Hour)}
	v := ViewOf(&s, "0.1.0-dev.42")
	if v.Target == nil || v.Target.Version != "0.1.0-dev.43" || v.LastSuccess == nil || v.NextCheck == nil || v.Installed != "0.1.0-dev.42" {
		t.Fatalf("欄位沒有帶出：%+v", v)
	}
	if v := ViewOf(&State{Stage: StageIdle}, ""); v.LastCheck != nil || v.LastSuccess != nil {
		t.Fatalf("沒有時間的欄位應省略：%+v", v)
	}
}

// 只檢查不下載：發現新版後狀態是 found，沒有任何下載、準備、切換。
func TestCheckRecordsResultWithoutDownloadingOrSwitching(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", running: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100),
		cond: Conditions{Idle: 10 * time.Minute, IdleKnown: true}}
	co, clk := newCoordinator(t, p)
	s, err := co.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.calls, " ") != "fetch" {
		t.Fatalf("只應查詢索引：%v", p.calls)
	}
	if s.Stage != StageFound || s.Target == nil || s.Target.Version != "0.1.0-dev.18" || !s.LastSuccess.Equal(clk.t) {
		t.Fatalf("%+v", s)
	}
	if s.Latest == nil || s.Latest.Version != "0.1.0-dev.18" || !s.Latest.Eligible || s.Latest.Reason != "" {
		t.Fatalf("Latest：%+v", s.Latest)
	}
	// 結果已保存：重新讀到相同內容。
	if back, err := Load(co.Base); err != nil || back.Stage != StageFound || back.Latest == nil || back.Latest.Version != "0.1.0-dev.18" {
		t.Fatalf("%+v %v", back, err)
	}
}

func TestCheckFailureIsRecordedNotReturned(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", fetchErr: errors.New("offline")}
	co, _ := newCoordinator(t, p)
	s, err := co.Check(context.Background())
	if err != nil {
		t.Fatalf("查詢失敗是結果，不是錯誤：%v", err)
	}
	if v := ViewOf(s, "0.1.0-dev.17"); v.State != CheckFailed || v.CheckFailures != 1 || !strings.Contains(v.Error, "offline") {
		t.Fatalf("%+v", v)
	}
}

func TestCheckSaysWhyAnUpdateIsNotOfferedYet(t *testing.T) {
	for _, c := range []struct {
		name   string
		feed   func() *fakePlatform
		reason string
		ok     bool
	}{
		{"同版", func() *fakePlatform {
			return &fakePlatform{installed: "0.1.0-dev.18", feed: feedFor("0.1.0-dev.18", "core", 100)}
		}, "", false},
		{"索引較舊", func() *fakePlatform {
			return &fakePlatform{installed: "0.1.0-dev.18", feed: feedFor("0.1.0-dev.17", "core", 100)}
		}, "", false},
		{"分批還沒輪到", func() *fakePlatform {
			return &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 0)}
		}, ReasonRollout, false},
		{"已撤回", func() *fakePlatform {
			return &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100, "0.1.0-dev.18")}
		}, ReasonWithdrawn, false},
		{"沒有此平台的安裝包", func() *fakePlatform {
			f := feedFor("0.1.0-dev.18", "core", 100)
			f.Assets[0].OS = "windows"
			return &fakePlatform{installed: "0.1.0-dev.17", feed: f}
		}, ReasonNoAsset, false},
		{"可更新", func() *fakePlatform {
			return &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
		}, "", true},
	} {
		co, _ := newCoordinator(t, c.feed())
		s, err := co.Check(context.Background())
		if err != nil || s.Latest == nil {
			t.Fatalf("%s：%+v %v", c.name, s, err)
		}
		if s.Latest.Reason != c.reason || s.Latest.Eligible != c.ok {
			t.Errorf("%s：Latest %+v，應為 reason=%q eligible=%v", c.name, s.Latest, c.reason, c.ok)
		}
	}
	// 這台電腦上健康檢查失敗過的版本：不再自動重試，原因要說清楚。
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
	co, _ := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Failed = []string{"0.1.0-dev.18"}
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	if s, err := co.Check(context.Background()); err != nil || s.Latest.Reason != ReasonFailed || s.Latest.Eligible {
		t.Fatalf("%+v %v", s.Latest, err)
	}
}

// 切換或回復進行中不重新判斷版本，交給背景協調器收尾。
func TestCheckRefusesWhileASwitchIsInProgress(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.18", "core", 100)}
	co, _ := newCoordinator(t, p)
	s, _ := Load(co.Base)
	s.Switched = true
	if err := Save(co.Base, s); err != nil {
		t.Fatal(err)
	}
	if _, err := co.Check(context.Background()); !errors.Is(err, ErrUpdateInProgress) || len(p.calls) != 0 {
		t.Fatalf("%v %v", err, p.calls)
	}
}

func TestWithLockSerializesAndReleases(t *testing.T) {
	base := t.TempDir()
	ran := 0
	if err := WithLock(base, "settings-write.lock", func() error {
		ran++
		// 持有期間別人拿不到鎖。
		if err := WithLock(base, "settings-write.lock", func() error { ran++; return nil }); !errors.Is(err, ErrLocked) {
			t.Errorf("應回 ErrLocked：%v", err)
		}
		return nil
	}); err != nil || ran != 1 {
		t.Fatalf("%v ran=%d", err, ran)
	}
	// 釋放後可再取得；fn 的錯誤原樣回傳。
	want := errors.New("boom")
	if err := WithLock(base, "settings-write.lock", func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("%v", err)
	}
}

func TestCheckWaitsForBackgroundCoordinator(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17", feed: feedFor("0.1.0-dev.17", "core", 100)}
	co, _ := newCoordinator(t, p)
	unlock, err := lock(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { time.Sleep(60 * time.Millisecond); unlock(); close(done) }()
	defer func() { <-done }()
	if _, err := co.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.calls, " ") != "fetch" {
		t.Fatalf("%v", p.calls)
	}
}

func TestCheckBusyAndCancellationDoNotFetch(t *testing.T) {
	p := &fakePlatform{installed: "0.1.0-dev.17"}
	co, _ := newCoordinator(t, p)
	unlock, err := lock(co.Base)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := co.Check(context.Background()); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := co.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if len(p.calls) != 0 {
		t.Fatalf("%v", p.calls)
	}
}

func TestLockIOErrorsAreNotBusy(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update", "settings-write.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	err := WithLock(base, "settings-write.lock", func() error { t.Fatal("must not run"); return nil })
	if err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("directory collision must be IO: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "update", "coordinator.lock"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	// An invalid base must retain the underlying filesystem error as well.
	_, err = lockFile(filepath.Join(base, "update", "coordinator.lock"), "test.lock")
	if err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("invalid base: %v", err)
	}
}

// 設定程式以完整文字辨識「仍開著舊版外殼」（#98），兩邊要一起改。
func TestSettingsUIRecognizesOldShellDetail(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "platforms", "settings", "ui", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "const OLD_SHELL = '"+OldShellDetail+"';") {
		t.Fatalf("app.js does not match OldShellDetail %q", OldShellDetail)
	}
}
