//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func present(p string) bool { _, err := os.Lstat(p); return err == nil }

func writeBundledAgent(t *testing.T, app string) string {
	t.Helper()
	p := bundledAgentPlist(app)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// #213：原版 .inactive fixture 必須精確還原；完整 App 的檔案／名稱不可再改。
func TestRestoreBackupAgentsPreservesCompleteBundles(t *testing.T) {
	backups := t.TempDir()
	for _, name := range []string{"macos-20261020-115117-1", "macos-20261019-010101-2", "someone-else"} {
		app := filepath.Join(backups, name, "0c.app")
		p := writeBundledAgent(t, app)
		if err := os.Rename(p, p+inactiveAgentSuffix); err != nil {
			t.Fatal(err)
		}
	}
	healthy := filepath.Join(backups, "macos-20261021-010101-3", "0c.app")
	p := writeBundledAgent(t, healthy)
	before, _ := os.Stat(p)
	if n := restoreBackupAgents(backups); n != 2 {
		t.Fatalf("restored %d, want 2", n)
	}
	for _, name := range []string{"macos-20261020-115117-1", "macos-20261019-010101-2"} {
		p := bundledAgentPlist(filepath.Join(backups, name, "0c.app"))
		b, err := os.ReadFile(p)
		if err != nil || string(b) != "<plist/>" || present(p+inactiveAgentSuffix) {
			t.Fatal("original bytes and filename must be restored")
		}
	}
	after, _ := os.Stat(p)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("complete bundle must remain untouched")
	}
	if !present(bundledAgentPlist(filepath.Join(backups, "someone-else", "0c.app")) + inactiveAgentSuffix) {
		t.Fatal("not an installation backup")
	}
	if n := restoreBackupAgents(backups); n != 0 {
		t.Fatal("restoration must be idempotent")
	}
	if n := restoreBackupAgents(filepath.Join(backups, "absent")); n != 0 {
		t.Fatal("absent backups")
	}
}

func TestRestoreBackupAgentsWaitsForInstallLock(t *testing.T) {
	support := t.TempDir()
	backups := filepath.Join(support, "backups")
	app := filepath.Join(backups, "macos-20261020-115117-1", "0c.app")
	p := writeBundledAgent(t, app)
	os.Rename(p, p+inactiveAgentSuffix)
	lock := filepath.Join(support, "update.lock")
	os.WriteFile(lock, []byte("1\n"), 0o600)
	if n := restoreBackupAgents(backups); n != 0 || present(p) {
		t.Fatal("leave a running transaction alone")
	}
	if !bundlesAgent(app) {
		t.Fatal("renamed agent still counts")
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(lock, old, old)
	if n := restoreBackupAgents(backups); n != 1 || !bundlesAgent(app) {
		t.Fatal("stale lock does not block repair")
	}
}

func TestRestoreBundledAgentSkipsSymlinkAndExistingFile(t *testing.T) {
	app := filepath.Join(t.TempDir(), "0c.app")
	p := writeBundledAgent(t, app)
	if restoreBundledAgent(app) {
		t.Fatal("complete bundle is not modified")
	}
	os.WriteFile(p+inactiveAgentSuffix, []byte("other"), 0o644)
	if restoreBundledAgent(app) {
		t.Fatal("existing plist is never overwritten")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "<plist/>" {
		t.Fatal("original plist changed")
	}
	os.Remove(p)
	os.Remove(p + inactiveAgentSuffix)
	target := filepath.Join(t.TempDir(), "elsewhere.plist")
	os.WriteFile(target, []byte("x"), 0o644)
	if err := os.Symlink(target, p+inactiveAgentSuffix); err != nil {
		t.Fatal(err)
	}
	if restoreBundledAgent(app) || present(p) {
		t.Fatal("only regular legacy files can be restored")
	}
}

func TestAgentSpawnFailed(t *testing.T) {
	broken := "gui/501/com.biggo.0c.update = {\n\tstate = spawn scheduled\n\tlast exit code = 78: EX_CONFIG\n\tjob state = spawn failed\n}\n"
	if !agentSpawnFailed(broken) {
		t.Fatal("launchd could not spawn the job (its app was deleted): re-register")
	}
	ok := "gui/501/com.biggo.0c.update = {\n\tstate = not running\n\tlast exit code = 0\n\tjob state = exited\n}\n"
	if agentSpawnFailed(ok) {
		t.Fatal("a job that ran is fine")
	}
	if agentSpawnFailed("\tlast exit code = 1\n\tjob state = exited\n") {
		t.Fatal("our own failures are not launchd spawn failures")
	}
}

func TestKickAfterAdoption(t *testing.T) {
	cases := []struct {
		name           string
		pulled         bool
		marker, runs   int
		running, inJob bool
		want           agentKick
	}{
		{"pulled from the menu, job idle: run the new updater now and remember", true, -1, 6, false, false, agentKick{true, 6}},
		{"pulled while the old updater is still running: wait for it", true, -1, 6, true, false, agentKick{false, 6}},
		{"old updater finished: kick now", false, 6, 6, false, false, agentKick{true, 6}},
		{"launchd ignored the kick (just exited): ask again", false, 6, 6, false, false, agentKick{true, 6}},
		{"kicked run in progress: wait", false, 6, 7, true, false, agentKick{false, 6}},
		{"a run finished after the marker: done", false, 6, 7, false, false, agentKick{false, -1}},
		{"the new updater in the job clears the marker", true, 6, 6, false, true, agentKick{false, -1}},
		{"nothing pulled, no marker", false, -1, 6, false, false, agentKick{false, -1}},
	}
	for _, c := range cases {
		if got := kickAfterAdoption(c.pulled, c.marker, c.runs, c.running, c.inJob); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	out := "\tactive count = 1\n\tstate = running\n\truns = 12\n"
	if !agentRunning(out) || agentRunning("\tstate = not running\n") || agentRuns(out) != 12 || agentRuns("") != 0 {
		t.Fatal("launchctl print state and runs")
	}
}

func TestAgentAdoption(t *testing.T) {
	cases := []struct {
		name string
		o    agentObservation
		want agentAdoption
	}{
		{"steady state: nothing could have pulled the registration", agentObservation{State: "enabled"}, adoptNothing},
		{"a installed bundle was just restored: pull the registration back", agentObservation{Restored: true, State: "enabled"}, adoptPull},
		{"restored inside the job itself: pulling is safe", agentObservation{Restored: true, State: "enabled", InJob: true}, adoptPull},
		{"user turned it off in System Settings: leave it", agentObservation{Restored: true, State: "requires-approval"}, adoptNothing},
		{"launchd cannot spawn it (backup deleted): re-register", agentObservation{Broken: true, State: "enabled"}, adoptReregister},
		{"never unregister from inside the job", agentObservation{Broken: true, State: "enabled", InJob: true}, adoptNothing},
		{"broken and disabled by the user: leave it", agentObservation{Broken: true, State: "requires-approval"}, adoptNothing},
		{"old rollback put back a quieted app: register again", agentObservation{Restored: true, State: "not-registered"}, adoptPull},
		{"unless the user removed the schedule", agentObservation{Restored: true, State: "not-registered", Removed: true}, adoptNothing},
		{"not registered for other reasons: the app's own monitor handles it", agentObservation{State: "not-registered"}, adoptNothing},
	}
	for _, c := range cases {
		if got := c.o.adoption(); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if (agentObservation{State: "enabled"}).needsState() {
		t.Error("the menu's 15-second status must not launch the app when nothing changed")
	}
	if !(agentObservation{Broken: true}).needsState() || !(agentObservation{Restored: true}).needsState() {
		t.Error("query the state when something may need fixing")
	}
}

// 舊輸入法程式的選單每 15 秒查一次 status：沒有要轉移的跡象時不必每次都執行 launchctl print。剛改名、改回 plist
// （要拉回登記）、有 kick 記號、check／auto／finish 時照舊每次看，#205 背景更新後跟到備份的登記仍會當場拉回。
func TestNeedsLaunchdCheck(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-time.Minute), now.Add(-agentCheckInterval)
	cases := []struct {
		name       string
		foreground bool
		o          agentObservation
		pending    int
		last       time.Time
		want       bool
	}{
		{"menu status, nothing changed, checked a minute ago: skip", false, agentObservation{}, -1, recent, false},
		{"menu status, never checked", false, agentObservation{}, -1, time.Time{}, true},
		{"menu status, last check one interval ago: look for spawn failed", false, agentObservation{}, -1, old, true},
		{"clock went backwards", false, agentObservation{}, -1, now.Add(time.Hour), true},
		{"completed handover must schedule the new updater now", false, agentObservation{Rebound: true}, -1, recent, true},
		{"rollback put back a quieted app", false, agentObservation{Restored: true}, -1, recent, true},
		{"kick marker waiting for the new updater to run", false, agentObservation{}, 6, recent, true},
		{"kick marker recorded at runs 0", false, agentObservation{}, 0, recent, true},
		{"check/auto/finish/schedule adopt always look", true, agentObservation{}, -1, recent, true},
	}
	for _, c := range cases {
		if got := needsLaunchdCheck(c.foreground, c.o, c.pending, c.last, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}

	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !launchdCheckDue(base, false, agentObservation{}, -1, time.Now()) {
		t.Fatal("no stamp yet: check")
	}
	noteLaunchdCheck(base)
	if launchdCheckDue(base, false, agentObservation{}, -1, time.Now()) {
		t.Fatal("just checked: the next menu status skips launchctl print")
	}
	if !launchdCheckDue(base, false, agentObservation{}, -1, time.Now().Add(agentCheckInterval)) {
		t.Fatal("an interval later: check again")
	}
	if !launchdCheckDue(base, false, agentObservation{Rebound: true}, -1, time.Now()) {
		t.Fatal("a fresh backup must not wait for the interval")
	}
}
