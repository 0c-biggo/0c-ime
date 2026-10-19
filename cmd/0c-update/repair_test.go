package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

// fakeRepair 模擬防毒軟體移除穩定更新器與排程：install 放回檔案並建立排程，quarantine 再移除。
type fakeRepair struct {
	base     string
	now      time.Time
	schedule bool
	installs int
	notices  []autoupdate.Notice
}

func (f *fakeRepair) hooks() repairHooks {
	return repairHooks{
		scheduleExists: func() bool { return f.schedule },
		install: func(base string) error {
			f.installs++
			f.schedule = true
			return os.WriteFile(stableUpdater(base), []byte("updater"), 0o700)
		},
		notify: func(n autoupdate.Notice) error { f.notices = append(f.notices, n); return nil },
		now:    func() time.Time { return f.now },
	}
}

func (f *fakeRepair) quarantine(t *testing.T) {
	t.Helper()
	if err := os.Remove(stableUpdater(f.base)); err != nil {
		t.Fatal(err)
	}
	f.schedule = false
}

func newFakeRepair(t *testing.T) *fakeRepair {
	t.Helper()
	f := &fakeRepair{base: t.TempDir(), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), schedule: true}
	if err := os.MkdirAll(filepath.Join(f.base, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stableUpdater(f.base), []byte("updater"), 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRepairUpdaterRebuildsOnceThenStopsAndNotifies(t *testing.T) {
	f := newFakeRepair(t)
	if err := repairUpdater(f.base, true, f.hooks()); err != nil || f.installs != 0 {
		t.Fatalf("healthy install must be left alone: err=%v installs=%d", err, f.installs)
	}

	f.quarantine(t)
	if err := repairUpdater(f.base, true, f.hooks()); err != nil || f.installs != 1 || len(f.notices) != 0 {
		t.Fatalf("first removal must be repaired silently: err=%v installs=%d notices=%d", err, f.installs, len(f.notices))
	}

	// 兩天後又被移除：同一個檔案會再被隔離，不再重建，改通知（附說明頁）。
	f.now = f.now.Add(48 * time.Hour)
	f.quarantine(t)
	err := repairUpdater(f.base, true, f.hooks())
	if err == nil || f.installs != 1 || len(f.notices) != 1 {
		t.Fatalf("repeated removal must stop and notify: err=%v installs=%d notices=%d", err, f.installs, len(f.notices))
	}
	if !strings.Contains(f.notices[0].Body, AntivirusHelpURL) {
		t.Fatalf("notice must link the help page: %q", f.notices[0].Body)
	}
	s, _ := autoupdate.Load(f.base)
	if s.UpdaterBroken != problemUpdaterMissing {
		t.Fatalf("broken state = %q", s.UpdaterBroken)
	}

	// 同一週內不重複通知。
	f.now = f.now.Add(time.Hour)
	if repairUpdater(f.base, true, f.hooks()) == nil || len(f.notices) != 1 {
		t.Fatalf("notice must not repeat within a week: %d", len(f.notices))
	}

	// 超過 7 天沒有新的重建紀錄：再試一次重建（例如防護定義已更新、誤判已撤銷）。
	f.now = f.now.Add(8 * 24 * time.Hour)
	if err := repairUpdater(f.base, true, f.hooks()); err != nil || f.installs != 2 {
		t.Fatalf("must retry after the window: err=%v installs=%d", err, f.installs)
	}
	s, _ = autoupdate.Load(f.base)
	if s.UpdaterBroken != "" {
		t.Fatalf("successful repair must clear the broken state")
	}
}

func TestRepairUpdaterScheduleOnlyAndOptOut(t *testing.T) {
	f := newFakeRepair(t)
	f.schedule = false
	if err := repairUpdater(f.base, false, f.hooks()); err != nil || f.installs != 1 {
		t.Fatalf("missing schedule must be rebuilt: err=%v installs=%d", err, f.installs)
	}

	// 使用者以 schedule remove 關閉：不修復、不通知。
	g := newFakeRepair(t)
	if err := setScheduleRemoved(g.base, true); err != nil {
		t.Fatal(err)
	}
	g.quarantine(t)
	if err := repairUpdater(g.base, true, g.hooks()); err != nil || g.installs != 0 || len(g.notices) != 0 {
		t.Fatalf("opted-out schedule must stay off: err=%v installs=%d notices=%d", err, g.installs, len(g.notices))
	}

	// 沒有 bin/（未建立排程的安裝，例如測試安裝）：不視為問題。
	h := &fakeRepair{base: t.TempDir(), now: time.Now()}
	if err := repairUpdater(h.base, true, h.hooks()); err != nil || h.installs != 0 {
		t.Fatalf("install without schedule must be left alone: err=%v installs=%d", err, h.installs)
	}
}

func TestRepairUpdaterFailedRebuildNotifies(t *testing.T) {
	f := newFakeRepair(t)
	f.quarantine(t)
	h := f.hooks()
	h.install = func(string) error { f.installs++; return os.ErrPermission }
	if err := repairUpdater(f.base, true, h); err == nil || len(f.notices) != 1 {
		t.Fatalf("failed rebuild must notify: err=%v notices=%d", err, len(f.notices))
	}
}
