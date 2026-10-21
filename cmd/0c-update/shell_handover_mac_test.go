//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/update"
)

const testIM = "/Users/lily/Library/Input Methods"

func TestRetirableMacShellsOnlyOursAndNotCurrent(t *testing.T) {
	backups := "/Users/lily/Library/Application Support/0c/backups"
	current := testIM + "/0c.app/Contents/MacOS/0c"
	ps := "  11 " + current + "\n" + // 換掉前的舊執行檔（同一路徑、inode 不同）：要結束
		"  12 " + current + "\n" + // 目前安裝的那一份：留著
		"  13 " + current + "\n" + // 一次性工具（--update-agent）：不碰
		"  14 " + testIM + "/Zuin.app/Contents/MacOS/Zuin\n" + // 0.1.0-dev.21 以前的 Zuin.app：要結束
		"  15 " + backups + "/macos-1/0c.app/Contents/MacOS/0c\n" + // 系統開到備份裡的舊 App（#98）：要結束
		"  16 /Applications/0c.app/Contents/MacOS/0c\n" + // 不是這個帳號的安裝：不碰
		"  17 /Users/other/Library/Input Methods/0c.app/Contents/MacOS/0c\n" + // 別的帳號：不碰
		"  18 " + current + "\n" + // 讀不到命令列：不碰
		"  19 " + current + "\n" + // inode 無法核對：不碰
		"  20 /usr/bin/0c-server\n"
	args := map[string]string{
		"11": current, "12": current, "13": current + " --update-agent status", "14": testIM + "/Zuin.app/Contents/MacOS/Zuin",
		"15": backups + "/macos-1/0c.app/Contents/MacOS/0c", "16": "/Applications/0c.app/Contents/MacOS/0c",
		"17": "/Users/other/Library/Input Methods/0c.app/Contents/MacOS/0c", "19": current, "20": "/usr/bin/0c-server",
	}
	isCurrent := func(pid string) (bool, error) {
		switch pid {
		case "12":
			return true, nil
		case "19":
			return false, errors.New("lsof failed")
		}
		return false, nil
	}
	got := retirableMacShells(macShellProcesses(ps), args, []string{testIM, backups}, current, isCurrent)
	var pids []uint32
	for _, p := range got {
		pids = append(pids, p.PID)
	}
	if want := []uint32{11, 14, 15}; !equalPIDs(pids, want) {
		t.Fatalf("retire %v, want %v", pids, want)
	}
	// 解除安裝：這個帳號安裝位置裡的輸入法程式全部結束（工具與讀不到命令列的仍不碰）。
	got = retirableMacShells(macShellProcesses(ps), args, []string{testIM, backups}, "", isCurrent)
	pids = nil
	for _, p := range got {
		pids = append(pids, p.PID)
	}
	if want := []uint32{11, 12, 14, 15, 19}; !equalPIDs(pids, want) {
		t.Fatalf("uninstall retires %v, want %v", pids, want)
	}
	// 前綴相同的別的目錄不算在安裝位置之下。
	if underAny(testIM+"-old/0c.app/Contents/MacOS/0c", []string{testIM}) {
		t.Fatal("prefix sibling counted as ours")
	}
}

func equalPIDs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// macShellRetire 模擬一個登入工作階段：使用者何時打字、引擎何時在組字、舊的 0c.app 收到 SIGTERM 後多久結束。
type macShellRetire struct {
	t, start  time.Time
	alive     map[uint32]bool
	exitAfter map[uint32]time.Duration // 收到 SIGTERM 後多久結束；負數表示不結束（新版 App 等停手）
	termAt    map[uint32]time.Duration
	lastKey   time.Duration // 最後一次按鍵
	composing func(at time.Duration) bool
	terms     []time.Duration
}

func (m *macShellRetire) at() time.Duration { return m.t.Sub(m.start) }

func (m *macShellRetire) control() uiControl {
	return uiControl{
		idle: func() time.Duration {
			if m.composing != nil && m.composing(m.at()) {
				return 0
			}
			return m.at() - m.lastKey
		},
		close: func(p uiProcess) error {
			if !m.alive[p.PID] {
				return errors.New("gone")
			}
			m.terms = append(m.terms, m.at())
			if _, ok := m.termAt[p.PID]; !ok {
				m.termAt[p.PID] = m.at()
			}
			return nil
		},
		exited: func(p uiProcess, wait time.Duration) bool {
			after := m.exitAfter[p.PID]
			if after < 0 || m.termAt[p.PID]+after > m.at()+wait {
				m.t = m.t.Add(wait)
				return false
			}
			m.t = m.start.Add(m.termAt[p.PID] + after)
			m.alive[p.PID] = false
			return true
		},
		sleep: func(d time.Duration) { m.t = m.t.Add(d) },
		now:   func() time.Time { return m.t },
	}
}

func (m *macShellRetire) pending() []uiProcess {
	var out []uiProcess
	for _, pid := range []uint32{11, 12} {
		if m.alive[pid] {
			out = append(out, uiProcess{PID: pid, Path: testIM + "/0c.app/Contents/MacOS/0c"})
		}
	}
	return out
}

func newMacShellRetire() *macShellRetire {
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	return &macShellRetire{t: now, start: now, alive: map[uint32]bool{11: true}, exitAfter: map[uint32]time.Duration{11: 30 * time.Millisecond},
		termAt: map[uint32]time.Duration{}, lastKey: -time.Hour}
}

// 正在組字時不結束；組完字、使用者停手 uiQuiet 之後才送 SIGTERM，不掉字。
func TestRetireMacShellWaitsForCompositionAndQuiet(t *testing.T) {
	m := newMacShellRetire()
	m.composing = func(at time.Duration) bool { return at < 2*time.Second }
	m.lastKey = 2 * time.Second // 組字到 2 秒時按 Enter 送出
	retired, left := retireProcesses(m.control(), m.pending, 10*time.Second)
	if retired != 1 || len(left) != 0 {
		t.Fatalf("retired=%d left=%v", retired, left)
	}
	if len(m.terms) != 1 || m.terms[0] < 2*time.Second+uiQuiet {
		t.Fatalf("SIGTERM at %v; must wait for the composition to end and %v of quiet", m.terms, uiQuiet)
	}
}

// 一直在打字：到期仍不結束，留給下一次排程喚起；不升級成強制結束。
func TestRetireMacShellLeavesBusyShell(t *testing.T) {
	m := newMacShellRetire()
	ctl := m.control()
	idle := ctl.idle
	ctl.idle = func() time.Duration { m.lastKey = m.at() - 100*time.Millisecond; return idle() }
	retired, left := retireProcesses(ctl, m.pending, 10*time.Second)
	if retired != 0 || len(left) != 1 || len(m.terms) != 0 {
		t.Fatalf("retired=%d left=%v terms=%v", retired, left, m.terms)
	}
}

// 新版 App 收到 SIGTERM 後自己再等到閒下來：沒等到就留著，下一次再送。
func TestRetireMacShellNewAppDefersTerm(t *testing.T) {
	m := newMacShellRetire()
	m.exitAfter[11] = -1
	retired, left := retireProcesses(m.control(), m.pending, 5*time.Second)
	if retired != 0 || len(left) != 1 || len(m.terms) < 1 {
		t.Fatalf("retired=%d left=%v terms=%v", retired, left, m.terms)
	}
}

// 背景交接（#178）：使用者在電腦前時，停手多久都不關——結束舊程式後已開著的 App 第一個按鍵會漏成英文；
// 螢幕鎖定或關閉之後才關，回來時的解鎖就讓 App 連上新版。等待中途使用者回來就停下。
func TestRetireMacShellOnlyWhenAway(t *testing.T) {
	m := newMacShellRetire()
	m.lastKey = 0
	awayFrom, awayUntil := 40*time.Second, time.Duration(1<<62)
	away := func() bool { return m.at() >= awayFrom && m.at() < awayUntil }
	ctl := awayOnly(m.control(), away)
	ctl.quiet = 2 * time.Second
	// 30 秒沒按鍵，但人還在（沒鎖定）：不關。
	retired, left := retireProcesses(ctl, m.pending, 30*time.Second)
	if retired != 0 || len(left) != 1 || len(m.terms) != 0 {
		t.Fatalf("present but idle: retired=%d left=%v terms=%v", retired, left, m.terms)
	}
	// 40 秒時鎖定螢幕：下一輪就關。
	retired, left = retireProcesses(ctl, m.pending, 30*time.Second)
	if retired != 1 || len(left) != 0 || len(m.terms) != 1 || m.terms[0] < awayFrom {
		t.Fatalf("away: retired=%d left=%v terms=%v", retired, left, m.terms)
	}

	// 鎖定之後很快解鎖回來：還沒送出就停下，留給下一次離開。
	m = newMacShellRetire()
	m.lastKey = 0
	awayFrom, awayUntil = 0, 0
	ctl = awayOnly(m.control(), away)
	ctl.quiet = 2 * time.Second
	if retired, left := retireProcesses(ctl, m.pending, 30*time.Second); retired != 0 || len(left) != 1 || len(m.terms) != 0 {
		t.Fatalf("came back: retired=%d left=%v terms=%v", retired, left, m.terms)
	}
}

// 使用者在不在每次要跑 ioreg：retireProcesses 每 200 毫秒一輪、每個程序各問一次，以前等 30 秒要跑三百次；
// 現在最多每秒一次。
func TestAwayOnlyChecksAtMostOncePerSecond(t *testing.T) {
	m := newMacShellRetire()
	m.alive[12], m.exitAfter[12] = true, 30*time.Millisecond
	m.lastKey = 0
	calls := 0
	away := func() bool { calls++; return false } // 人一直在：不關，等滿 30 秒
	ctl := awayOnly(m.control(), away)
	ctl.quiet = 2 * time.Second
	if retired, left := retireProcesses(ctl, m.pending, 30*time.Second); retired != 0 || len(left) != 2 {
		t.Fatalf("retired=%d left=%v", retired, left)
	}
	if calls > 32 {
		t.Fatalf("30 秒內問了 %d 次", calls)
	}
}

// 舊程式全部結束後才啟動新版（同一個 bundle ID 的兩份 App 會搶 IMK 連線名稱）；沒有結束任何程式時不啟動。
func TestHandOverShellsRelaunchesOnlyAfterAllRetired(t *testing.T) {
	m := newMacShellRetire()
	launched := 0
	relaunch := func() { launched++ }
	if retired, left := handOverShells(m.control(), m.pending, 10*time.Second, relaunch); retired != 1 || len(left) != 0 || launched != 1 {
		t.Fatalf("retired=%d left=%v launched=%d", retired, left, launched)
	}
	// 已經沒有舊程式：不再啟動。
	if retired, _ := handOverShells(m.control(), m.pending, 10*time.Second, relaunch); retired != 0 || launched != 1 {
		t.Fatalf("nothing to retire: retired=%d launched=%d", retired, launched)
	}
	// 兩個舊程式，其中一個（新版 App 在組字）沒結束：不啟動。
	m = newMacShellRetire()
	m.alive[12], m.exitAfter[12] = true, -1
	if retired, left := handOverShells(m.control(), m.pending, 5*time.Second, relaunch); retired != 1 || len(left) != 1 || launched != 1 {
		t.Fatalf("one left: retired=%d left=%v launched=%d", retired, left, launched)
	}
}

// IOConsoleUsers 取自 macOS 15.7.7（tart VM）：平常、螢幕保護程式（未設密碼也標示鎖定）；另加快速切換使用者與
// 全名裡有逗號、大括號的情況。
func TestMacConsoleAway(t *testing.T) {
	const (
		present = `    | {
      "IOConsoleLocked" = No
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kSCSecuritySessionID"=100003,"kCGSSessionSystemSafeBoot"=No,"kCGSessionLoginDoneKey"=Yes,"kCGSSessionIDKey"=257,"kCGSSessionUserNameKey"="admin","kCGSSessionGroupIDKey"=20,"CGSSessionUniqueSessionUUID"="F2F33480-6B38-4AEC-BA5F-75E436E6FA28","kCGSessionLongUserNameKey"="Managed via Tart","kCGSSessionAuditIDKey"=100003,"kCGSSessionLoginwindowSafeLogin"=No,"kCGSSessionUserIDKey"=501})
      "IOKitDiagnostics" = {"Instance allocation"=2436264}
`
		locked = `      "IOConsoleLocked" = No
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kSCSecuritySessionID"=100003,"kCGSSessionSystemSafeBoot"=No,"kCGSessionLoginDoneKey"=Yes,"kCGSSessionIDKey"=257,"kCGSSessionUserNameKey"="admin","kCGSSessionGroupIDKey"=20,"CGSSessionUniqueSessionUUID"="F2F33480-6B38-4AEC-BA5F-75E436E6FA28","CGSSessionScreenLockedTime"=3939993391,"kCGSessionLongUserNameKey"="Managed via Tart","CGSSessionScreenIsLocked"=Yes,"kCGSSessionAuditIDKey"=100003,"kCGSSessionSecureInputPID"=154,"kCGSSessionLoginwindowSafeLogin"=No,"kCGSSessionUserIDKey"=501})
`
		// 501 切到別的使用者（502）：501 不在主控台。全名帶逗號與大括號、還有巢狀的值。
		switched = `      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=No,"kCGSessionLongUserNameKey"="Lee, Roger {Mac}","kCGSSessionUserIDKey"=501,"nested"=("a,b",{"x"=1})},{"kCGSSessionOnConsoleKey"=Yes,"kCGSessionLongUserNameKey"="Other, User","kCGSSessionUserIDKey"=502})
`
		lockedNamed = `      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kCGSessionLongUserNameKey"="A, B} \"C\"","CGSSessionScreenIsLocked"=Yes,"kCGSSessionUserIDKey"=501})
`
	)
	for _, tc := range []struct {
		name string
		out  string
		uid  int
		want bool
	}{
		{"使用中", present, 501, false},
		{"螢幕保護程式／鎖定", locked, 501, true},
		{"切換到別的使用者", switched, 501, true},
		{"另一個使用者在主控台", switched, 502, false},
		{"全名有特殊字元", lockedNamed, 501, true},
		{"沒有自己的工作階段", present, 502, false},
		{"沒有 IOConsoleUsers", "", 501, false},
		{"輸出被截斷", `"IOConsoleUsers" = ({"CGSSessionScreenIsLocked"=Yes,"kCGSSessionUserIDKey"=501`, 501, false},
	} {
		if got := macConsoleAway(tc.out, tc.uid); got != tc.want {
			t.Errorf("%s: away=%v, want %v (sessions %v)", tc.name, got, tc.want, macConsoleSessions(tc.out))
		}
	}
}

func TestMacKeepsRightShift(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prev    *update.MacReceipt
		existed bool
		want    bool
	}{
		{"全新安裝", nil, false, false},
		{"沒有收據的原型 App", nil, true, true},
		{"0.2.0", &update.MacReceipt{Version: "0.2.0"}, true, true},
		{"0.1.0-dev.64", &update.MacReceipt{Version: "0.1.0-dev.64"}, true, true},
		{"0.2.1 的候選版", &update.MacReceipt{Version: "0.2.1-rc.1"}, true, true},
		{"已移除後重新安裝", &update.MacReceipt{Version: "0.2.0", Active: false}, false, true},
		{"0.2.1", &update.MacReceipt{Version: "0.2.1"}, true, false},
		{"0.2.2", &update.MacReceipt{Version: "0.2.2"}, true, false},
		{"收據沒有版本", &update.MacReceipt{}, true, true},
	} {
		if got := macKeepsRightShift(tc.prev, tc.existed); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 背景更新由 0.2.0 的更新器安裝時，新版的程式碼事後以收據與備份判斷。
func TestMacUpgradedFromRightShift(t *testing.T) {
	write := func(t *testing.T, path string, v any) {
		t.Helper()
		b, _ := json.Marshal(v)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name      string
		current   string
		prev      *update.MacReceipt // nil：全新安裝時備份的 previous.json 是 null
		backupApp bool
		noBackup  bool
		want      bool
	}{
		{"0.2.0 升級到 0.2.1", "0.2.1", &update.MacReceipt{Version: "0.2.0", Active: true}, true, false, true},
		{"全新安裝 0.2.1", "0.2.1", nil, false, false, false},
		{"沒有收據的原型升級", "0.2.1", nil, true, false, true},
		{"0.2.1 升級到 0.2.2", "0.2.2", &update.MacReceipt{Version: "0.2.1", Active: true}, true, false, false},
		{"回復到 0.2.0", "0.2.0", &update.MacReceipt{Version: "0.2.1", Active: true}, true, false, false},
		{"0.2.0 升級到 0.2.1 的候選版", "0.2.1-rc.1", &update.MacReceipt{Version: "0.2.0", Active: true}, true, false, true},
		{"備份已被清掉", "0.2.1", nil, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			support := t.TempDir()
			write(t, filepath.Join(support, "install-receipt.json"), update.MacReceipt{Version: tc.current, App: update.MacApp, Active: true, Previous: "backups/macos-1"})
			dir := filepath.Join(support, "backups/macos-1")
			if !tc.noBackup {
				write(t, filepath.Join(dir, "previous.json"), tc.prev)
			}
			if tc.backupApp {
				os.MkdirAll(filepath.Join(dir, update.MacApp, "Contents"), 0o700)
			}
			if got := macUpgradedFromRightShift(support); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMacShellHelper(t *testing.T) {
	for args, want := range map[string]bool{
		testIM + "/0c.app/Contents/MacOS/0c":                                   false,
		testIM + "/0c.app/Contents/MacOS/0c --update-notice":                   true,
		testIM + "/0c.app/Contents/MacOS/0c --activate-input-source /x/0c.app": true,
		testIM + "/0c.app/Contents/MacOS/0c --confirm-input-source /x/0c.app":  true,
		testIM + "/0c.app/Contents/MacOS/0c --update-agent status":             true,
	} {
		if macShellHelper(args) != want {
			t.Errorf("%q: want %v", args, want)
		}
	}
}
