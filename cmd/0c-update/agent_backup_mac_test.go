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

// #205：macOS 15 上，被移入備份、仍在執行的舊輸入法程式每次查詢 SMAppService 都會把背景項目的登記拉到它所在的備份，
// 排程就一直執行備份裡的舊 0c-update。備份裡的 App 沒有內附排程 plist 時查詢不會拉走登記，所以把它改名。
func TestQuietBackupAgentsRenamesOnlyBackupPlists(t *testing.T) {
	backups := t.TempDir()
	old := filepath.Join(backups, "macos-20261020-115117-454278212", "0c.app")
	older := filepath.Join(backups, "macos-20261019-010101-1", "0c.app")
	stranger := filepath.Join(backups, "someone-else", "0c.app") // 不是安裝備份的命名，不動
	for _, app := range []string{old, older, stranger} {
		writeBundledAgent(t, app)
	}
	if n := quietBackupAgents(backups); n != 2 {
		t.Fatalf("quieted %d backups, want 2", n)
	}
	for _, app := range []string{old, older} {
		if present(bundledAgentPlist(app)) || !present(bundledAgentPlist(app)+inactiveAgentSuffix) {
			t.Fatalf("%s: bundled plist must be renamed out of the way", app)
		}
	}
	if !present(bundledAgentPlist(stranger)) {
		t.Fatal("directories not named like install backups are left alone")
	}
	if n := quietBackupAgents(backups); n != 0 {
		t.Fatalf("second pass quieted %d, want 0 (nothing new)", n)
	}
	if n := quietBackupAgents(filepath.Join(backups, "missing")); n != 0 {
		t.Fatal("missing backups directory is not an error")
	}
}

// 回復進行中（安裝鎖）時不改名：選單每 15 秒的 status 會在回復把 plist 改回之後、核對之前又改掉（VM 實測）。
func TestQuietBackupAgentsWaitsForInstallLock(t *testing.T) {
	support := t.TempDir()
	backups := filepath.Join(support, "backups")
	app := filepath.Join(backups, "macos-20261020-115117-1", "0c.app")
	writeBundledAgent(t, app)
	lock := filepath.Join(support, "update.lock")
	os.WriteFile(lock, []byte("1\n"), 0o600)
	if n := quietBackupAgents(backups); n != 0 || !present(bundledAgentPlist(app)) {
		t.Fatal("an install or rollback in progress: leave the backups alone")
	}
	if !bundlesAgent(app) {
		t.Fatal("bundlesAgent sees the plist")
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(lock, old, old)
	if n := quietBackupAgents(backups); n != 1 {
		t.Fatal("a lock left over from an interrupted update long ago does not block")
	}
	if !bundlesAgent(app) || bundlesAgent(filepath.Join(backups, "none")) {
		t.Fatal("bundlesAgent counts the renamed plist too")
	}
}

func TestQuietBackupAgentsSkipsSymlinkedPlist(t *testing.T) {
	backups := t.TempDir()
	app := filepath.Join(backups, "macos-20261020-115117-1", "0c.app")
	target := filepath.Join(t.TempDir(), "elsewhere.plist")
	os.WriteFile(target, []byte("x"), 0o644)
	p := bundledAgentPlist(app)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if n := quietBackupAgents(backups); n != 0 {
		t.Fatal("only regular files are renamed")
	}
}

// 回復（rollback／恢復）把備份裡的 App 放回 Input Methods 之前先改回原名，放回的 App 與收據、簽章一致，排程照常登記。
func TestRestoreBundledAgent(t *testing.T) {
	backups := t.TempDir()
	app := filepath.Join(backups, "macos-20261020-115117-1", "0c.app")
	p := writeBundledAgent(t, app)
	quietBackupAgents(backups)
	if !restoreBundledAgent(app) {
		t.Fatal("renamed plist must be restored")
	}
	if !present(p) || present(p+inactiveAgentSuffix) {
		t.Fatal("plist back under its own name")
	}
	if restoreBundledAgent(app) {
		t.Fatal("nothing to restore the second time")
	}
	// 兩個都在（不該發生）時不覆蓋現有的 plist。
	os.WriteFile(p+inactiveAgentSuffix, []byte("other"), 0o644)
	if restoreBundledAgent(app) {
		t.Fatal("an existing plist is never overwritten")
	}
	if b, _ := os.ReadFile(p); string(b) != "<plist/>" {
		t.Fatal("existing plist kept")
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
		{"a backup was just quieted: pull the registration back", agentObservation{Quieted: 1, State: "enabled"}, adoptPull},
		{"quieted inside the job itself: pulling is safe", agentObservation{Quieted: 2, State: "enabled", InJob: true}, adoptPull},
		{"user turned it off in System Settings: leave it", agentObservation{Quieted: 1, State: "requires-approval"}, adoptNothing},
		{"launchd cannot spawn it (backup deleted): re-register", agentObservation{Broken: true, State: "enabled"}, adoptReregister},
		{"never unregister from inside the job", agentObservation{Broken: true, State: "enabled", InJob: true}, adoptNothing},
		{"broken and disabled by the user: leave it", agentObservation{Broken: true, State: "requires-approval"}, adoptNothing},
		{"old rollback put back a quieted app: register again", agentObservation{Restored: true, State: "not-registered"}, adoptPull},
		{"unless the user removed the schedule", agentObservation{Restored: true, State: "not-registered", Removed: true}, adoptNothing},
		{"not registered for other reasons: the app's own monitor handles it", agentObservation{Quieted: 1, State: "not-registered"}, adoptNothing},
	}
	for _, c := range cases {
		if got := c.o.adoption(); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if (agentObservation{State: "enabled"}).needsState() {
		t.Error("the menu's 15-second status must not launch the app when nothing changed")
	}
	if !(agentObservation{Broken: true}).needsState() || !(agentObservation{Quieted: 1}).needsState() || !(agentObservation{Restored: true}).needsState() {
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
		{"background update left a backup to quiet: pull it back now", false, agentObservation{Quieted: 1}, -1, recent, true},
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
	if !launchdCheckDue(base, false, agentObservation{Quieted: 1}, -1, time.Now()) {
		t.Fatal("a fresh backup must not wait for the interval")
	}
}
