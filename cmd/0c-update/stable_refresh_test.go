package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// stableFixture：目前版本的更新器（新版）與穩定副本（舊版）。
type stableFixture struct {
	source, want, stable string
}

func newStableFixture(t *testing.T) stableFixture {
	t.Helper()
	root := t.TempDir()
	f := stableFixture{source: filepath.Join(root, "versions", "new", "0c-update"), stable: filepath.Join(root, "bin", "0c-update")}
	for path, content := range map[string]string{f.source: "new updater", f.stable: "old updater"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.want = hashOf(t, f.source)
	return f
}

func hashOf(t *testing.T, path string) string {
	t.Helper()
	h, err := update.Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// copyTo 是測試用的 reinstall：只換穩定副本。
func copyTo(stable string, calls *int) func(string) error {
	return func(source string) error {
		*calls++
		return replaceUpdater(source, stable)
	}
}

func TestRefreshStaleUpdaterReplacesAnOlderStableCopy(t *testing.T) {
	f := newStableFixture(t)
	calls := 0
	refreshed, err := refreshStaleUpdater(f.source, f.want, f.stable, copyTo(f.stable, &calls))
	if err != nil || !refreshed || calls != 1 {
		t.Fatalf("refreshed=%v err=%v calls=%d", refreshed, err, calls)
	}
	if got := read(t, f.stable); got != "new updater" {
		t.Fatalf("stable copy is %q", got)
	}
	refreshed, err = refreshStaleUpdater(f.source, f.want, f.stable, copyTo(f.stable, &calls))
	if err != nil || refreshed || calls != 1 {
		t.Fatalf("an identical copy must be left alone: refreshed=%v err=%v calls=%d", refreshed, err, calls)
	}
}

// 來源與安裝紀錄的雜湊不符（被改過、不完整）時不換，也不重建排程。
func TestRefreshStaleUpdaterRefusesAnUnverifiedSource(t *testing.T) {
	f := newStableFixture(t)
	if err := os.WriteFile(f.source, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if refreshed, err := refreshStaleUpdater(f.source, f.want, f.stable, copyTo(f.stable, &calls)); err == nil || refreshed || calls != 0 {
		t.Fatalf("refreshed=%v err=%v calls=%d", refreshed, err, calls)
	}
	if got := read(t, f.stable); got != "old updater" {
		t.Fatalf("stable copy changed to %q", got)
	}
}

func TestRefreshStaleUpdaterLeavesAMissingOrUnknownSituationAlone(t *testing.T) {
	f := newStableFixture(t)
	calls := 0
	if refreshed, err := refreshStaleUpdater("", "", f.stable, copyTo(f.stable, &calls)); refreshed || err != nil {
		t.Fatalf("no known current updater: refreshed=%v err=%v", refreshed, err)
	}
	if err := os.Remove(f.stable); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := refreshStaleUpdater(f.source, f.want, f.stable, copyTo(f.stable, &calls)); refreshed || err != nil || calls != 0 {
		t.Fatalf("a missing stable copy is the antivirus repair's job: refreshed=%v err=%v", refreshed, err)
	}
	if _, err := os.Stat(f.stable); !os.IsNotExist(err) {
		t.Fatal("the refresh must not create the stable copy")
	}
}

// 目前版本：有引擎指標時取核心目錄記錄的更新器（核心沒有記錄就不換，不退回較舊的完整安裝版本）；
// 沒有指標時取有效、啟用中的收據。
func TestCurrentUpdaterFollowsThePointerThenTheReceipt(t *testing.T) {
	base := t.TempDir()
	receipt := filepath.Join(base, "receipt.json")
	writeReceipt := func(dir string, active bool) {
		b, _ := json.Marshal(map[string]any{"directory": dir, "active": active, "files": map[string]string{"0c-update.exe": "aa"}})
		if err := os.WriteFile(receipt, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeReceipt("versions/0.1.0-dev.44-abc", true)
	if p, h := currentUpdater(base, receipt, "0c-update.exe"); p != filepath.Join(base, "versions", "0.1.0-dev.44-abc", "0c-update.exe") || h != "aa" {
		t.Fatalf("receipt: %q %q", p, h)
	}
	for _, bad := range []struct {
		dir    string
		active bool
	}{{"versions/..", true}, {"../elsewhere", true}, {"versions/0.1.0-dev.44-abc", false}} {
		writeReceipt(bad.dir, bad.active)
		if p, _ := currentUpdater(base, receipt, "0c-update.exe"); p != "" {
			t.Fatalf("%+v must not be used: %q", bad, p)
		}
	}
	writeReceipt("versions/0.1.0-dev.44-abc", true)

	core := filepath.Join(base, "versions", "0.1.0-dev.45-c-123")
	if err := os.MkdirAll(core, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := enginedir.Write(base, enginedir.Current{Directory: "versions/0.1.0-dev.45-c-123", Version: "0.1.0-dev.45"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := currentUpdater(base, receipt, "0c-update.exe"); p != "" {
		t.Fatalf("a core without a recorded updater must not fall back to the older receipt: %q", p)
	}
	if err := enginedir.WriteFiles(core, map[string]string{"0c-update.exe": "bb"}); err != nil {
		t.Fatal(err)
	}
	if p, h := currentUpdater(base, receipt, "0c-update.exe"); p != filepath.Join(core, "0c-update.exe") || h != "bb" {
		t.Fatalf("pointer: %q %q", p, h)
	}
}

func repairFixture(t *testing.T) (*fakeRepair, repairHooks) {
	f := newFakeRepair(t)
	f.now = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	return f, f.hooks()
}

func lastKind(t *testing.T, base string) string {
	t.Helper()
	s, err := autoupdate.Load(base)
	if err != nil || len(s.History) == 0 {
		t.Fatalf("history: %v %v", s, err)
	}
	return s.History[len(s.History)-1].Kind
}

func TestRepairRefreshesAStaleUpdaterWithoutCountingItAsARepair(t *testing.T) {
	f, h := repairFixture(t)
	calls := 0
	h.refreshStable = func(string) (bool, error) { calls++; return true, nil }
	if err := repairUpdater(f.base, true, h); err != nil {
		t.Fatal(err)
	}
	s, _ := autoupdate.Load(f.base)
	if calls != 1 || f.installs != 0 || len(s.UpdaterRepairs) != 0 || s.UpdaterBroken != "" || len(f.notices) != 0 {
		t.Fatalf("calls=%d installs=%d repairs=%v broken=%q notices=%v", calls, f.installs, s.UpdaterRepairs, s.UpdaterBroken, f.notices)
	}
	if got := lastKind(t, f.base); got != "updater_refreshed" {
		t.Fatalf("history ends with %q", got)
	}
}

func TestRepairRecordsAFailedRefreshAndStaysHealthy(t *testing.T) {
	f, h := repairFixture(t)
	h.refreshStable = func(string) (bool, error) { return false, os.ErrPermission }
	if err := repairUpdater(f.base, true, h); err != nil {
		t.Fatalf("a failed refresh must not report the updater as stopped: %v", err)
	}
	if got := lastKind(t, f.base); got != "updater_refresh_failed" {
		t.Fatalf("history ends with %q", got)
	}
	if len(f.notices) != 0 {
		t.Fatalf("notices: %v", f.notices)
	}
}

func TestRepairDoesNotRefreshWhenTheUserRemovedTheSchedule(t *testing.T) {
	f, h := repairFixture(t)
	if _, err := autoupdate.EditState(f.base, func(s *autoupdate.State) error { s.ScheduleRemoved = true; return nil }); err != nil {
		t.Fatal(err)
	}
	h.refreshStable = func(string) (bool, error) {
		t.Fatal("refresh called although the user removed the schedule")
		return false, nil
	}
	if err := repairUpdater(f.base, true, h); err != nil {
		t.Fatal(err)
	}
}

// 換新一直失敗時，同樣的失敗只記一次；成功之後再失敗才再記。
func TestRepairRecordsARepeatedRefreshFailureOnce(t *testing.T) {
	f, h := repairFixture(t)
	fail := errors.New("0c-update.exe.old is in use")
	h.refreshStable = func(string) (bool, error) { return false, fail }
	for range 5 {
		if err := repairUpdater(f.base, true, h); err != nil {
			t.Fatal(err)
		}
	}
	count := func(kind string) int {
		s, _ := autoupdate.Load(f.base)
		n := 0
		for _, e := range s.History {
			if e.Kind == kind {
				n++
			}
		}
		return n
	}
	if n := count("updater_refresh_failed"); n != 1 {
		t.Fatalf("a repeated failure was recorded %d times", n)
	}
	h.refreshStable = func(string) (bool, error) { return true, nil }
	repairUpdater(f.base, true, h)
	h.refreshStable = func(string) (bool, error) { return false, fail }
	repairUpdater(f.base, true, h)
	if n := count("updater_refresh_failed"); n != 2 {
		t.Fatalf("a failure after a success must be recorded again: %d", n)
	}
}

// 換新期間協調器存下的進度不能被 repair 用舊快照蓋掉。
func TestRepairKeepsStateWrittenDuringTheRefresh(t *testing.T) {
	f, h := repairFixture(t)
	h.refreshStable = func(string) (bool, error) {
		if _, err := autoupdate.EditState(f.base, func(s *autoupdate.State) error {
			s.Stage = autoupdate.StagePrepared
			s.Record("prepared", "")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return true, nil
	}
	if err := repairUpdater(f.base, true, h); err != nil {
		t.Fatal(err)
	}
	s, _ := autoupdate.Load(f.base)
	if s.Stage != autoupdate.StagePrepared || lastKind(t, f.base) != "updater_refreshed" {
		t.Fatalf("stage=%q history=%v", s.Stage, s.History)
	}
}
