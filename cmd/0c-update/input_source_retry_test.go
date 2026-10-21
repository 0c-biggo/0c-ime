//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// #195 後續：input-source 記號留著時，排程（約 15 分鐘一次）不能每次都跑 --activate-input-source。啟用失敗時
// （使用者按「不允許」或沒理系統詢問、新的輸入模式要重新登入才登記得到）每次執行都會再跳一次詢問（.12 實測），
// 所以同一個登入工作階段只試一次，跨過登入邊界再試，連續 inputSourceRetrySessions 個工作階段失敗就停止。

type retryFixture struct {
	t     *testing.T
	base  string
	calls int
	fail  error
	// enabled：不補做時查詢到的狀態（使用者自己在系統設定加入、或之後才按「允許」）；queries 是查詢次數。
	enabled bool
	queries int
	t0      time.Time
	boot    time.Time
}

func newRetryFixture(t *testing.T) *retryFixture {
	t.Helper()
	home := t.TempDir()
	app := filepath.Join(home, "Library/Input Methods/0c.app")
	tool := filepath.Join(app, "Contents/MacOS/0c")
	base := filepath.Join(home, "Library/Application Support/0c")
	for _, dir := range []string{filepath.Dir(tool), filepath.Join(base, "update")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(tool, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputSourceMarker(base), []byte(tool+"\n"+app+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	return &retryFixture{t: t, base: base, t0: t0, boot: t0.Add(-time.Hour)}
}

// run 模擬一次排程喚起：目前的登入工作階段 asid，距安裝 minutes 分鐘。
func (f *retryFixture) run(asid uint64, minutes int) error {
	return retryInputSource(f.base, loginStamp{ASID: asid, At: f.t0.Add(time.Duration(minutes) * time.Minute)}, f.boot, func(string, string, ...bool) error {
		f.calls++
		return f.fail
	}, func(string, string) bool {
		f.queries++
		return f.enabled
	})
}

func (f *retryFixture) pending() bool { return onDisk(inputSourceMarker(f.base)) }

func onDisk(p string) bool { _, err := os.Stat(p); return err == nil }

func TestInputSourceRetryStaleMarkerTriesRightAway(t *testing.T) {
	// 0.2.3 以前留下、沒有補做紀錄的記號（#195 回報者）：0.2.4 的第一次排程就補做，同一個工作階段就清掉。
	f := newRetryFixture(t)
	if err := f.run(7, 0); err != nil || f.calls != 1 || f.pending() || onDisk(inputSourceRetryFile(f.base)) {
		t.Fatalf("err=%v calls=%d pending=%v", err, f.calls, f.pending())
	}
}

func TestInputSourceRetryOncePerLoginThenStops(t *testing.T) {
	f := newRetryFixture(t)
	f.fail = errors.New("已要求啟用，但系统尚未啟用 (exit status 2)")
	// 安裝當下在工作階段 7 試過、沒成功（deferInputSource 記下）。
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 7, At: f.t0})
	for m := 15; m <= 24*60; m += 15 { // 同一個工作階段一整天的排程喚起都不再跑啟用工具
		if err := f.run(7, m); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls != 0 {
		t.Fatalf("same login session must not retry, ran %d times", f.calls)
	}
	// 重新登入（工作階段 8）：補做一次，失敗後這個工作階段不再試。
	if err := f.run(8, 24*60+1); err == nil || f.calls != 1 {
		t.Fatalf("new login retries once: err=%v calls=%d", err, f.calls)
	}
	for m := 24*60 + 15; m <= 48*60; m += 15 {
		_ = f.run(8, m)
	}
	if f.calls != 1 {
		t.Fatalf("retried %d times in the second login session", f.calls)
	}
	// 重新開機算跨過登入邊界（ASID 可能剛好相同）；之後取不到 ASID、也沒再開機時不算。
	f.boot = f.t0.Add(49 * time.Hour)
	if _ = f.run(8, 50*60); f.calls != 2 {
		t.Fatalf("reboot retries: calls=%d", f.calls)
	}
	if _ = f.run(0, 50*60+15); f.calls != 2 {
		t.Fatalf("no ASID and no reboot since the last try: calls=%d", f.calls)
	}
	// 第三個工作階段也失敗：停止，記號留給狀態摘要，記進更新紀錄。
	err := f.run(9, 51*60)
	if err == nil || !strings.Contains(err.Error(), "不再自動重試") || f.calls != 3 {
		t.Fatalf("third failed login stops: err=%v calls=%d", err, f.calls)
	}
	r := loadInputSourceRetry(f.base)
	if !r.stopped() || r.Failed != inputSourceRetrySessions || r.Error != f.fail.Error() || !f.pending() {
		t.Fatalf("stopped record: %+v pending=%v", r, f.pending())
	}
	s, err := autoupdate.Load(f.base)
	if err != nil || len(s.History) == 0 || s.History[len(s.History)-1].Kind != "input_source_retry_stopped" {
		t.Fatalf("history: %+v %v", s, err)
	}
	for asid := uint64(10); asid < 20; asid++ {
		_ = f.run(asid, int(asid)*24*60)
	}
	if f.calls != 3 {
		t.Fatalf("stopped retries still ran: calls=%d", f.calls)
	}
}

func TestInputSourceRetrySucceedsAfterLogin(t *testing.T) {
	// 0.2.3 以前的情形：安裝時要重新登入才登記得到，登入後第一次排程就啟用成功，記號與紀錄都清掉。
	f := newRetryFixture(t)
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 7, At: f.t0})
	if err := f.run(7, 15); err != nil || f.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, f.calls)
	}
	if err := f.run(8, 60); err != nil || f.calls != 1 || f.pending() || onDisk(inputSourceRetryFile(f.base)) {
		t.Fatalf("err=%v calls=%d pending=%v", err, f.calls, f.pending())
	}
	// 之後的安裝又沒啟用成功時 deferInputSource 記下新的紀錄（Failed 0），停止過的也重新起算。
	r := inputSourceRetry{ASID: 9, At: f.t0}
	if next, _ := r.session().crossed(10, f.boot); !next {
		t.Fatal("a fresh install record retries at the next login")
	}
	if same, _ := r.session().crossed(9, f.boot); same {
		t.Fatal("a fresh install record does not retry in the same login")
	}
}

func TestInputSourceRetryRecordUnreadableOrAppGone(t *testing.T) {
	f := newRetryFixture(t)
	if err := os.WriteFile(inputSourceRetryFile(f.base), []byte("{garbled"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.run(7, 0); err != nil || f.calls != 1 {
		t.Fatalf("an unreadable record behaves like none: err=%v calls=%d", err, f.calls)
	}
	f = newRetryFixture(t)
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 7, At: f.t0, Failed: 1})
	b, _ := os.ReadFile(inputSourceMarker(f.base))
	if err := os.RemoveAll(strings.TrimSpace(strings.Split(string(b), "\n")[1])); err != nil {
		t.Fatal(err)
	}
	if err := f.run(8, 60); err != nil || f.calls != 0 || f.pending() || onDisk(inputSourceRetryFile(f.base)) {
		t.Fatalf("app gone: err=%v calls=%d pending=%v", err, f.calls, f.pending())
	}
}

// 安裝時取不到 ASID（記成 0）：當時確定沒有圖形登入（headless，例如只從 SSH 安裝）時，之後取得到 ASID 就是登入過了，
// 算跨過登入邊界，跟 0.2.3 以前登入時補做一樣；兩邊都取不到、也沒重新開機時不算。relogin-required 用同一個判斷。
func TestInputSourceRetryHeadlessInstallRetriesAfterLogin(t *testing.T) {
	f := newRetryFixture(t)
	f.fail = errors.New("已要求啟用，但系統尚未啟用 (exit status 2)")
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 0, At: f.t0, Headless: true})
	if err := f.run(0, 15); err != nil || f.calls != 0 {
		t.Fatalf("still no login session: err=%v calls=%d", err, f.calls)
	}
	if err := f.run(7, 60); err == nil || f.calls != 1 {
		t.Fatalf("first login after a headless install retries: err=%v calls=%d", err, f.calls)
	}
	if _ = f.run(7, 75); f.calls != 1 {
		t.Fatalf("the failed retry is recorded with ASID 7, same session must not retry: calls=%d", f.calls)
	}
	if r := loadInputSourceRetry(f.base); r.ASID != 7 || r.Headless {
		t.Fatalf("retry record: %+v", r)
	}
}

// 2b9a7cd 把「記下 0、現在非 0」一律當成跨過登入：在圖形工作階段裡安裝、只是當時 launchctl 取不到 ASID 的，同一個
// 工作階段就會再跑一次啟用（再跳一次詢問），relogin-required 也會被清掉（誤報已生效）。當時有圖形登入、或舊紀錄
// 不知道時，第一次取得 ASID 只記下來，之後 ASID 改變才算跨過。
func TestInputSourceRetryGUIInstallWithoutASIDWaitsForNextLogin(t *testing.T) {
	f := newRetryFixture(t)
	f.fail = errors.New("已要求啟用，但系統尚未啟用 (exit status 2)")
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 0, At: f.t0}) // 舊紀錄沒有 headless，或當時有圖形登入
	for m := 15; m <= 120; m += 15 {
		if err := f.run(7, m); err != nil || f.calls != 0 {
			t.Fatalf("same GUI session must not retry: m=%d err=%v calls=%d", m, err, f.calls)
		}
	}
	if r := loadInputSourceRetry(f.base); r == nil || r.ASID != 7 || r.Failed != 0 {
		t.Fatalf("the first ASID seen is adopted: %+v", r)
	}
	if err := f.run(8, 24*60); err == nil || f.calls != 1 {
		t.Fatalf("next login retries: err=%v calls=%d", err, f.calls)
	}
	for _, c := range []struct {
		stored, current uint64
		headless        bool
		crossed, adopt  bool
	}{
		{0, 0, false, false, false}, {0, 0, true, false, false},
		{0, 7, true, true, false}, {0, 7, false, false, true},
		{7, 0, false, false, false}, {7, 7, false, false, false}, {7, 8, false, true, false},
	} {
		crossed, adopt := loginStamp{ASID: c.stored, At: f.t0, Headless: c.headless}.crossed(c.current, f.boot)
		if crossed != c.crossed || adopt != c.adopt {
			t.Errorf("stored %d headless %v current %d: crossed %v adopt %v, want %v %v", c.stored, c.headless, c.current, crossed, adopt, c.crossed, c.adopt)
		}
	}
	if crossed, _ := (loginStamp{At: f.t0}).crossed(7, f.t0.Add(time.Hour)); !crossed {
		t.Error("a reboot since the stamp always crosses")
	}
}

// relogin-required 用同一個判斷：在圖形工作階段安裝、當時沒記到 ASID 的，同一個工作階段仍要重新登入，下一次登入才清掉；
// 確定沒有圖形登入時安裝的，第一次有 ASID 就清掉。
func TestReloginMarkerSessionWithoutASID(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	boot := time.Now().Add(-time.Hour)
	write := func(s loginStamp) {
		if err := os.WriteFile(reloginMarker(base), formatReloginMarker(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(loginStamp{}) // 有圖形登入但 launchctl 失敗；0.2.3 寫的「0」也一樣
	for i := 0; i < 3; i++ {
		if !reloginRequired(base, 7, boot) {
			t.Fatal("same GUI session still requires a relogin")
		}
	}
	if b, _ := os.ReadFile(reloginMarker(base)); string(b) != "7\n" {
		t.Fatalf("adopted marker = %q", b)
	}
	if reloginRequired(base, 8, boot) || onDisk(reloginMarker(base)) {
		t.Fatal("the next login clears the marker")
	}
	write(loginStamp{Headless: true})
	if b, _ := os.ReadFile(reloginMarker(base)); string(b) != "0\nheadless\n" {
		t.Fatalf("headless marker = %q", b)
	}
	if !reloginRequired(base, 0, boot) {
		t.Fatal("still no login session")
	}
	if reloginRequired(base, 7, boot) {
		t.Fatal("the first login after a headless install clears the marker")
	}
}

func TestMacGUILogin(t *testing.T) {
	const session = `+-o Root  <class IORegistryEntry, id 0x100000100, retain 32>
    {
      "IOKitBuildVersion" = "Darwin Kernel Version 25.6.0"
      "IOConsoleUsers" = ({"kCGSSessionOnConsoleKey"=Yes,"kCGSSessionUserNameKey"="kevin","CGSSessionScreenIsLocked"=Yes,"kCGSSessionAuditIDKey"=124131,"kCGSSessionUserIDKey"=501})
    }
`
	for _, c := range []struct {
		name       string
		ioreg      string
		uid        int
		has, known bool
	}{
		{"own session (locked)", session, 501, true, true},
		{"someone else's session", session, 502, false, true},
		{"nobody logged in", "+-o Root  <class IORegistryEntry>\n    {\n      \"IOKitBuildVersion\" = \"x\"\n    }\n", 501, false, true},
		{"empty list", "+-o Root\n  \"IOConsoleUsers\" = ()\n", 501, false, true},
		{"garbled", "+-o Root\n  \"IOConsoleUsers\" = ({\"kCGSSessionUserIDKey\"=501", 501, false, false},
		{"no output", "", 501, false, false},
	} {
		if has, known := macGUILogin(c.ioreg, c.uid); has != c.has || known != c.known {
			t.Errorf("%s: has %v known %v, want %v %v", c.name, has, known, c.has, c.known)
		}
	}
}

// 停止補做後使用者自己到「系統設定 → 鍵盤 → 文字輸入」加入靈犀注音：之後的喚起只查詢（不跑啟用工具、不跳詢問），
// 查到已啟用就清掉記號與紀錄，狀態摘要不再顯示停止補做與舊的錯誤。同一個工作階段之後才按「允許」也一樣。
func TestInputSourceRetryClearsOnceEnabledElsewhere(t *testing.T) {
	f := newRetryFixture(t)
	f.fail = errors.New("已要求啟用，但系統尚未啟用 (exit status 2)")
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 9, At: f.t0, Failed: inputSourceRetrySessions, Error: f.fail.Error()})
	if err := f.run(10, 60); err != nil || f.calls != 0 || f.queries != 1 || !f.pending() {
		t.Fatalf("stopped, not enabled yet: err=%v calls=%d queries=%d pending=%v", err, f.calls, f.queries, f.pending())
	}
	f.enabled = true
	if err := f.run(10, 75); err != nil || f.calls != 0 || f.pending() || onDisk(inputSourceRetryFile(f.base)) {
		t.Fatalf("enabled by the user: err=%v calls=%d pending=%v", err, f.calls, f.pending())
	}

	f = newRetryFixture(t)
	saveInputSourceRetry(f.base, inputSourceRetry{ASID: 7, At: f.t0}) // 安裝當下試過、沒成功
	f.enabled = true                                                  // 之後才在系統詢問按「允許」
	if err := f.run(7, 15); err != nil || f.calls != 0 || f.queries != 1 || f.pending() {
		t.Fatalf("allowed later in the same session: err=%v calls=%d queries=%d pending=%v", err, f.calls, f.queries, f.pending())
	}
}

func TestConfirmedInputSource(t *testing.T) {
	for out, want := range map[string]bool{
		"com.biggo.inputmethod.0c enabled\ncom.biggo.inputmethod.0c.Bopomofo enabled\n":                                             true,
		"com.biggo.inputmethod.0c disabled\ncom.biggo.inputmethod.0c.Bopomofo enabled\n":                                            true, // 父項要等「允許」，模式啟用即可
		"com.biggo.inputmethod.0c enabled\ncom.biggo.inputmethod.0c.Bopomofo enabled\ncom.biggo.inputmethod.0c.Bopomofo selected\n": true,
		"com.biggo.inputmethod.0c enabled\ncom.biggo.inputmethod.0c.Bopomofo disabled\n":                                            false,
		"com.biggo.inputmethod.0c enabled\ncom.biggo.inputmethod.0c.Bopomofo missing\n":                                             false,
		"org.zuin.inputmethod.Zuin enabled\n":                                                                                       true, // 回復到沒有模式的版本
		"":                                                                                                                          false,
		"不是靈犀注音的 App\n":                                                                                                             false,
	} {
		if got := confirmedInputSource(out); got != want {
			t.Errorf("confirmedInputSource(%q) = %v, want %v", out, got, want)
		}
	}
}
