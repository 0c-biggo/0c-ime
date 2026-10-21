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
	t0    time.Time
	boot  time.Time
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
	return retryInputSource(f.base, asid, f.boot, f.t0.Add(time.Duration(minutes)*time.Minute), func(string, string, ...bool) error {
		f.calls++
		return f.fail
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
	if r := (inputSourceRetry{ASID: 9, At: f.t0}); !r.due(10, f.boot) || r.due(9, f.boot) {
		t.Fatal("a fresh install record retries at the next login only")
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
