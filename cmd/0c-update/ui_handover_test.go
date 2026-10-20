package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uiPath(base, dir string) string { return filepath.Join(base, "versions", dir, "0c-ui.exe") }

func TestOutdatedUIOnlyOursAndNotCurrent(t *testing.T) {
	base := filepath.Join("users", "a", "Zuin")
	other := filepath.Join("users", "b", "Zuin")
	current := uiPath(base, "0.2.1-x")
	running := []uiProcess{
		{PID: 1, Path: uiPath(base, "0.2.0-y")},                        // 舊版：要退出
		{PID: 2, Path: strings.ToUpper(current)},                       // 目前版本（大小寫不同）：留著
		{PID: 3, Path: uiPath(other, "0.2.0-y")},                       // 其他帳號自己的安裝：不碰
		{PID: 4, Path: ""},                                             // 讀不到路徑：不碰
		{PID: 5, Path: filepath.Join(base+"-old", "0c-ui.exe")},        // 前綴相同的別的目錄：不碰
		{PID: 6, Path: filepath.Join(base, "versions", "zuin-ui.exe")}, // 0.1.0-dev.15 以前：要退出
	}
	got := outdatedUI(running, base, current)
	if len(got) != 2 || got[0].PID != 1 || got[1].PID != 6 {
		t.Fatalf("outdatedUI = %+v", got)
	}
	// 解除安裝：這個安裝的全部退出。
	got = outdatedUI(running, base, "")
	if len(got) != 3 || got[0].PID != 1 || got[1].PID != 2 || got[2].PID != 6 {
		t.Fatalf("outdatedUI（解除安裝）= %+v", got)
	}
}

// fakeUI 模擬一個工作階段：候選窗何時顯示、使用者何時打字、收到 WM_CLOSE 後多久結束。
type fakeUI struct {
	t       time.Time
	start   time.Time
	running map[uint32]*fakeUIProc
	idle    func(at time.Duration) time.Duration
	closes  []uint32
}

type fakeUIProc struct {
	uiProcess
	visible   func(at time.Duration) bool
	exitAfter time.Duration // 收到 WM_CLOSE 之後多久結束；負數表示這段期間不結束
	closedAt  *time.Duration
}

func newFakeUI() *fakeUI {
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	return &fakeUI{t: now, start: now, running: map[uint32]*fakeUIProc{}, idle: func(time.Duration) time.Duration { return time.Hour }}
}

func (f *fakeUI) at() time.Duration { return f.t.Sub(f.start) }

func (f *fakeUI) reap() {
	for pid, p := range f.running {
		if p.closedAt != nil && p.exitAfter >= 0 && f.at() >= *p.closedAt+p.exitAfter {
			delete(f.running, pid)
		}
	}
}

func (f *fakeUI) control() uiControl {
	return uiControl{
		list: func() []uiProcess {
			f.reap()
			var out []uiProcess
			for pid := uint32(1); pid < 100; pid++ {
				if p, ok := f.running[pid]; ok {
					q := p.uiProcess
					q.Visible = p.visible != nil && p.visible(f.at())
					out = append(out, q)
				}
			}
			return out
		},
		idle: func() time.Duration { return f.idle(f.at()) },
		close: func(p uiProcess) error {
			f.closes = append(f.closes, p.PID)
			if q, ok := f.running[p.PID]; ok && q.closedAt == nil {
				at := f.at()
				q.closedAt = &at
			}
			return nil
		},
		exited: func(p uiProcess, wait time.Duration) bool {
			q, ok := f.running[p.PID]
			if !ok {
				return true
			}
			if q.exitAfter < 0 || *q.closedAt+q.exitAfter > f.at()+wait {
				f.t = f.t.Add(wait)
				return false
			}
			f.t = f.start.Add(*q.closedAt + q.exitAfter)
			f.reap()
			return true
		},
		sleep: func(d time.Duration) { f.t = f.t.Add(d) },
		now:   func() time.Time { return f.t },
	}
}

func TestRetireUIClosesOldWhenIdle(t *testing.T) {
	base := filepath.Join("u", "Zuin")
	f := newFakeUI()
	f.running[7] = &fakeUIProc{uiProcess: uiProcess{PID: 7, Path: uiPath(base, "old")}, exitAfter: 50 * time.Millisecond}
	f.running[8] = &fakeUIProc{uiProcess: uiProcess{PID: 8, Path: uiPath(base, "new")}}
	retired, left := retireUI(f.control(), base, uiPath(base, "new"), 10*time.Second)
	if retired != 1 || len(left) != 0 {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
	if len(f.closes) != 1 || f.closes[0] != 7 {
		t.Fatalf("WM_CLOSE 送給 %v，只該送給舊版（7）", f.closes)
	}
	if _, ok := f.running[8]; !ok {
		t.Fatal("目前版本的 0c-ui 不該結束")
	}
}

// 候選窗顯示著（使用者在選字）時不關：等它收起來之後才送 WM_CLOSE。
func TestRetireUIWaitsUntilCandidatesHidden(t *testing.T) {
	base := filepath.Join("u", "Zuin")
	f := newFakeUI()
	f.running[7] = &fakeUIProc{
		uiProcess: uiProcess{PID: 7, Path: uiPath(base, "old")},
		visible:   func(at time.Duration) bool { return at < 4*time.Second },
	}
	retired, left := retireUI(f.control(), base, uiPath(base, "new"), 10*time.Second)
	if retired != 1 || len(left) != 0 {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
	if p := f.running[7]; p != nil {
		t.Fatal("候選窗收起後應該結束")
	}
	if len(f.closes) != 1 {
		t.Fatalf("WM_CLOSE 次數 %d", len(f.closes))
	}
}

// 候選窗沒顯示，但使用者還在打字（下一個按鍵的顯示訊息可能正在路上）：等停手 uiQuiet 之後才關。
func TestRetireUIWaitsForInputPause(t *testing.T) {
	base := filepath.Join("u", "Zuin")
	f := newFakeUI()
	var closedAt time.Duration = -1
	f.running[7] = &fakeUIProc{uiProcess: uiProcess{PID: 7, Path: uiPath(base, "old")}}
	f.idle = func(at time.Duration) time.Duration {
		if at < 3*time.Second {
			return 100 * time.Millisecond // 一直在打字
		}
		return at - 3*time.Second
	}
	c := f.control()
	close := c.close
	c.close = func(p uiProcess) error { closedAt = f.at(); return close(p) }
	retired, _ := retireUI(c, base, uiPath(base, "new"), 10*time.Second)
	if retired != 1 {
		t.Fatalf("retired=%d", retired)
	}
	if closedAt < 3*time.Second+uiQuiet {
		t.Fatalf("在使用者停手 %v 之前就送了 WM_CLOSE（%v）", uiQuiet, closedAt)
	}
}

// 一直在用就不關：到期時回報仍在執行，留給下一次排程或登出；行程不被強制結束。
func TestRetireUIGivesUpWhileBusy(t *testing.T) {
	base := filepath.Join("u", "Zuin")
	f := newFakeUI()
	f.running[7] = &fakeUIProc{
		uiProcess: uiProcess{PID: 7, Path: uiPath(base, "old")},
		visible:   func(time.Duration) bool { return true },
	}
	retired, left := retireUI(f.control(), base, uiPath(base, "new"), 5*time.Second)
	if retired != 0 || len(left) != 1 || left[0].PID != 7 {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
	if len(f.closes) != 0 {
		t.Fatalf("顯示中不該送 WM_CLOSE：%v", f.closes)
	}
	if f.at() < 5*time.Second || f.at() > 6*time.Second {
		t.Fatalf("等待時間 %v", f.at())
	}
}

// 新版 0c-ui 收到 WM_CLOSE 後自己等到閒下來才結束；這次沒等到就回報仍在執行，不重複計數。
func TestRetireUIReportsDeferredExit(t *testing.T) {
	base := filepath.Join("u", "Zuin")
	f := newFakeUI()
	f.running[7] = &fakeUIProc{uiProcess: uiProcess{PID: 7, Path: uiPath(base, "old")}, exitAfter: -1}
	retired, left := retireUI(f.control(), base, uiPath(base, "new"), 4*time.Second)
	if retired != 0 || len(left) != 1 {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
}

func TestRetireUINothingRunning(t *testing.T) {
	f := newFakeUI()
	retired, left := retireUI(f.control(), "Zuin", uiPath("Zuin", "new"), time.Minute)
	if retired != 0 || left != nil || f.at() != 0 {
		t.Fatalf("retired=%d left=%v waited=%v", retired, left, f.at())
	}
}
