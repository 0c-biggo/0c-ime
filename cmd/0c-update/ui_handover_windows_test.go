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
