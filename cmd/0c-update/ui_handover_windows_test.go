package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 這裡的測試以測試程式自己扮演 0c-ui：複製到暫存的 versions\<名稱>\0c-ui.exe，以 ZC_TEST_UI_WINDOW 啟動時
// 建立 ZuinCandidateWindow 類別的視窗、照 0.2.0 的 0c-ui 處理 WM_CLOSE（交給 DefWindowProc → WM_DESTROY →
// 結束），不開任何管道，不會接到這台機器上 TSF DLL 的連線。

var (
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procShowWindow       = user32.NewProc("ShowWindow")
)

type wndClassEx struct {
	size, style                uint32
	wndProc                    uintptr
	clsExtra, wndExtra         int32
	instance, icon, cursor, bg windows.Handle
	menuName, className        *uint16
	iconSm                     windows.Handle
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
}

func TestUIWindowHelper(t *testing.T) {
	mode := os.Getenv("ZC_TEST_UI_WINDOW")
	if mode == "" {
		t.Skip("只在 ui_handover 測試啟動的子行程裡執行")
	}
	// 視窗的訊息佇列屬於建立它的執行緒：goroutine 換了執行緒，GetMessageW 就收不到 WM_CLOSE（.14 上 20 次約失敗 4 次）。
	runtime.LockOSThread()
	class, _ := windows.UTF16PtrFromString(uiWindowClass)
	proc := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		if msg == 0x0002 { // WM_DESTROY
			procPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	})
	wc := wndClassEx{wndProc: proc, className: class}
	wc.size = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	const exStyle = 0x00000080 | 0x08000000 | 0x00000008 // TOOLWINDOW | NOACTIVATE | TOPMOST，同 0c-ui
	var style uintptr = 0x80000000                       // WS_POPUP
	if mode == "visible" {
		style |= 0x10000000 // WS_VISIBLE：建立時就顯示（候選窗顯示中），列出時不會先看到隱藏的
	}
	h, _, err := procCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(class)), 0, style, 0, 0, 10, 10, 0, 0, 0, 0)
	if h == 0 {
		t.Fatal(err)
	}
	if mode == "visible" {
		// 第一次 ShowWindow 可能被啟動資訊（STARTUPINFO 的 SW_HIDE）取代，所以呼叫兩次。
		procShowWindow.Call(h, 4) // SW_SHOWNOACTIVATE
		procShowWindow.Call(h, 4)
	}
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// startFakeUI 把測試程式複製成 base\versions\<dir>\0c-ui.exe 並啟動，等它的視窗出現。
func startFakeUI(t *testing.T, base, dir, mode string) (*exec.Cmd, string) {
	t.Helper()
	exe := filepath.Join(base, "versions", dir, "0c-ui.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, exe)
	cmd := exec.Command(exe, "-test.run=^TestUIWindowHelper$")
	cmd.Env = append(os.Environ(), "ZC_TEST_UI_WINDOW="+mode)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	found := false
	for i := 0; i < 100; i++ {
		for _, p := range listUIWindows() {
			if p.PID == uint32(cmd.Process.Pid) {
				if p.Visible == (mode == "visible") {
					return cmd, exe
				}
				found = true
			}
		}
		if found && i >= 30 {
			// SSH 之類的非互動工作階段（不是 WinSta0）裡 ShowWindow 沒有作用，視窗一直是隱藏的。
			t.Skip("這個工作階段無法顯示視窗；在互動工作階段執行")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("候選窗沒有出現（%s）：%+v", mode, listUIWindows())
	return nil, ""
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
}

func idleControl() uiControl {
	c := windowsUIControl()
	c.idle = func() time.Duration { return time.Hour } // SSH 工作階段沒有使用者輸入可量
	return c
}

func exitedWithin(cmd *exec.Cmd, d time.Duration) bool {
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// 舊版（沒有退出指令的 0.2.0）0c-ui 收到 WM_CLOSE 就結束；目前版本的不動。
func TestRetireUIWindowsClosesOldKeepsCurrent(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Zuin")
	old, _ := startFakeUI(t, base, "0.2.0-old", "hidden")
	cur, current := startFakeUI(t, base, "0.2.1-new", "hidden")
	retired, left := retireUI(idleControl(), base, current, 10*time.Second)
	if retired != 1 || len(left) != 0 {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
	if !exitedWithin(old, 5*time.Second) {
		t.Fatal("舊版 0c-ui 沒有結束")
	}
	if exitedWithin(cur, 500*time.Millisecond) {
		t.Fatal("目前版本的 0c-ui 不該結束")
	}
}

// 候選窗顯示中（使用者在選字）不關；到期回報仍在執行，行程照常活著。
func TestRetireUIWindowsLeavesVisibleAlone(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Zuin")
	old, _ := startFakeUI(t, base, "0.2.0-old", "visible")
	retired, left := retireUI(idleControl(), base, filepath.Join(base, "versions", "new", "0c-ui.exe"), 2*time.Second)
	if retired != 0 || len(left) != 1 || left[0].PID != uint32(old.Process.Pid) || !left[0].Visible {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
	if exitedWithin(old, 300*time.Millisecond) {
		t.Fatal("顯示中的候選窗被關掉了")
	}
}

// 不是這個安裝目錄的候選窗程式（其他帳號自己的安裝、開發中的建置）不碰。
func TestRetireUIWindowsIgnoresOtherInstalls(t *testing.T) {
	root := t.TempDir()
	other, _ := startFakeUI(t, filepath.Join(root, "other", "Zuin"), "0.2.0-old", "hidden")
	retired, left := retireUI(idleControl(), filepath.Join(root, "Zuin"), "", 1*time.Second)
	if retired != 0 || left != nil {
		t.Fatalf("retired=%d left=%+v", retired, left)
	}
	if exitedWithin(other, 300*time.Millisecond) {
		t.Fatal("關到了別的安裝的 0c-ui")
	}
}

// useUIHandover 暫時把基底、收據與替換用的動作指向暫存目錄，回傳 restartUI／startUI 的呼叫紀錄。
func useUIHandover(t *testing.T, version string) (base string, restarts *int, started *[]string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	base = filepath.Join(root, "Zuin")
	exe := filepath.Join(base, "versions", version, "0c-ui.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := `{"version":"` + version + `","directory":"versions/` + version + `","active":true,"files":{"0c-ui.exe":"aa"}}`
	if err := os.WriteFile(filepath.Join(base, "install-receipt.json"), []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
	restarts, started = new(int), new([]string)
	savedRestart, savedStart, savedControl := restartUI, startUI, uiControlFor
	restartUI = func() { *restarts++ }
	startUI = func(exe string) error { *started = append(*started, exe); return nil }
	t.Cleanup(func() { restartUI, startUI, uiControlFor = savedRestart, savedStart, savedControl })
	return base, restarts, started
}

// #243：舊版 0c-ui 結束之後要馬上帶起新版；執行中的應用程式載入的舊 DLL 不會自己啟動，選單會沒有反應。
func TestHandOverUIRestartsAfterRetiring(t *testing.T) {
	base, restarts, _ := useUIHandover(t, "0.2.8")
	old, _ := startFakeUI(t, base, "0.2.7", "hidden")
	uiControlFor = idleControl
	handOverUI(10 * time.Second)
	if !exitedWithin(old, 5*time.Second) {
		t.Fatal("舊版 0c-ui 沒有結束")
	}
	if *restarts != 1 {
		t.Fatalf("restarts=%d，要請登入工作帶起新版一次", *restarts)
	}
}

// #243（0c-ime#42）：安裝時穩定副本還是舊的，0.2.7 的登入工作不會啟動 0c-ui，還會先帶起引擎，之後就沒有人再叫工作。
// install 只請舊版結束，afterInstall 做完才叫工作一次。
func TestInstallRestartsUIOnlyAfterTheStableCopyIsReplaced(t *testing.T) {
	t.Setenv("ZC_NO_SCHEDULE", "1")
	base, restarts, _ := useUIHandover(t, "0.2.8")
	old, _ := startFakeUI(t, base, "0.2.7", "hidden")
	uiControlFor = idleControl
	retireUIForInstall(10 * time.Second)
	if !exitedWithin(old, 5*time.Second) {
		t.Fatal("舊版 0c-ui 沒有結束")
	}
	if *restarts != 0 {
		t.Fatalf("穩定副本換新之前不能叫工作：restarts=%d", *restarts)
	}
	afterInstall()
	if *restarts != 1 {
		t.Fatalf("afterInstall 要帶起新版一次：restarts=%d", *restarts)
	}
	restartRetiredUI()
	if *restarts != 1 {
		t.Fatalf("不重複叫工作：restarts=%d", *restarts)
	}
}

// 沒有請走任何 0c-ui 的安裝，afterInstall 不為它多叫一次工作。
func TestAfterInstallDoesNotRestartWithoutRetiring(t *testing.T) {
	t.Setenv("ZC_NO_SCHEDULE", "1")
	_, restarts, _ := useUIHandover(t, "0.2.8")
	uiControlFor = func() uiControl {
		c := idleControl()
		c.list = func() []uiProcess { return nil }
		return c
	}
	retireUIForInstall(time.Second)
	afterInstall()
	if *restarts != 0 {
		t.Fatalf("restarts=%d", *restarts)
	}
}

// 沒有舊版要換（目前版本在跑，或根本沒有 0c-ui）時不多跑一次登入工作：登入工作自己每次也會呼叫 handOverUI。
func TestHandOverUIDoesNotRestartWithoutRetiring(t *testing.T) {
	_, restarts, _ := useUIHandover(t, "0.2.8")
	uiControlFor = func() uiControl {
		c := idleControl()
		c.list = func() []uiProcess { return nil }
		return c
	}
	handOverUI(time.Second)
	if *restarts != 0 {
		t.Fatalf("restarts=%d", *restarts)
	}
}

// 解除安裝結束 0c-ui 之後不能再把它帶起來。
func TestStopUIDoesNotRestart(t *testing.T) {
	base, restarts, _ := useUIHandover(t, "0.2.8")
	old, _ := startFakeUI(t, base, "0.2.7", "hidden")
	uiControlFor = idleControl
	stopUI(10 * time.Second)
	if !exitedWithin(old, 5*time.Second) {
		t.Fatal("0c-ui 沒有結束")
	}
	if *restarts != 0 {
		t.Fatalf("restarts=%d", *restarts)
	}
}

func TestEnsureUIStartsCurrentOnlyWhenNoneIsRunning(t *testing.T) {
	if elevatedOK() {
		t.Skip("ensureUI 不在提權的行程裡啟動 0c-ui；以一般權限執行（互動工作）才測得到")
	}
	base, _, started := useUIHandover(t, "0.2.8")
	running := []uiProcess{}
	uiControlFor = func() uiControl {
		c := idleControl()
		c.list = func() []uiProcess { return running }
		return c
	}
	ensureUI()
	want := filepath.Join(base, "versions", "0.2.8", "0c-ui.exe")
	if len(*started) != 1 || (*started)[0] != want {
		t.Fatalf("started=%v，要啟動 %s", *started, want)
	}
	running = []uiProcess{{PID: 1, Path: filepath.Join(base, "versions", "0.2.7", "0c-ui.exe")}}
	ensureUI()
	if len(*started) != 1 {
		t.Fatalf("已有 0c-ui 不該再啟動：%v", *started)
	}
}

// 提權的安裝程式不能啟動 0c-ui：它會以系統管理員權限常駐（同 startResidentEngine）。
func TestEnsureUIDoesNotStartElevated(t *testing.T) {
	if !elevatedOK() {
		t.Skip("要在提權的行程裡測")
	}
	_, _, started := useUIHandover(t, "0.2.8")
	uiControlFor = func() uiControl {
		c := idleControl()
		c.list = func() []uiProcess { return nil }
		return c
	}
	ensureUI()
	if len(*started) != 0 {
		t.Fatalf("started=%v", *started)
	}
}

// 沒有安裝時（解除安裝後的登入工作）什麼都不做。
func TestEnsureUINeedsAnActiveInstall(t *testing.T) {
	base, _, started := useUIHandover(t, "0.2.8")
	if err := os.Remove(filepath.Join(base, "install-receipt.json")); err != nil {
		t.Fatal(err)
	}
	ensureUI()
	if len(*started) != 0 {
		t.Fatalf("started=%v", *started)
	}
}
