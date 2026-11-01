//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 0c-ui 候選窗的視窗類別（platforms/windows/ui/src/main.rs）；0.1.0-dev.15 以前的 zuin-ui 也用同一個。
const uiWindowClass = "ZuinCandidateWindow"

const wmClose = 0x0010

var (
	procFindWindowExW = user32.NewProc("FindWindowExW")
	procPostMessageW  = user32.NewProc("PostMessageW")
)

// 交接等多久：安裝程式與「立即安裝」有人在等，短一點；登入工作與自動更新排程在背景，長一點。
const (
	uiWaitInstall = 10 * time.Second
	uiWaitTask    = 60 * time.Second
	uiWaitAuto    = 15 * time.Second
)

// handOverUI：安裝、回復之後，以及登入工作、自動更新排程每次喚起時，請本工作階段裡不是目前版本的 0c-ui 在閒置時結束；
// 新版由登入工作馬上帶起（restartUI），否則由 DLL 下一次需要時啟動。沒有啟用中的安裝、或目前版本的 0c-ui.exe 不在
// （DLL 啟動不了新的）時不動。
func handOverUI(wait time.Duration) {
	if retireOutdatedUI(wait) > 0 {
		restartUI()
	}
}

// retireOutdatedUI 回傳請走了幾個舊版 0c-ui。
func retireOutdatedUI(wait time.Duration) int {
	current, _ := installedFromReceipt(receiptPath(), basePath(), "0c-ui.exe")
	if current == "" {
		return 0
	}
	if _, err := os.Stat(current); err != nil {
		return 0
	}
	return retireAndReport(current, wait)
}

// uiRetiredByInstall：install 請走了舊版 0c-ui，等 afterInstall 換好穩定副本再帶起新版。
var uiRetiredByInstall bool

// retireUIForInstall（install）：只請舊版 0c-ui 結束。這時登入工作跑的還是舊的穩定副本：0.2.7 的 engine 不會啟動 0c-ui，
// 只會先把引擎帶起來，之後 afterInstall 看到引擎在跑就不再叫工作，0c-ui 就一直起不來（#243）。
func retireUIForInstall(wait time.Duration) {
	if retireOutdatedUI(wait) > 0 {
		uiRetiredByInstall = true
	}
}

// restartRetiredUI（afterInstall 的最後）：穩定副本換新之後，請登入工作帶起 install 請走的 0c-ui。引擎還在組字、沒有交接時
// 只有這裡會叫工作。ensureEngineService 剛叫過時工作不重複執行（IgnoreNew），它自己就會啟動 0c-ui。
func restartRetiredUI() {
	if uiRetiredByInstall {
		uiRetiredByInstall = false
		restartUI()
	}
}

// stopUI（解除安裝）：這個安裝的 0c-ui 全部在閒置時結束。仍載入著 DLL 的應用程式在登出前還用得到時會再啟動它。
func stopUI(wait time.Duration) { retireAndReport("", wait) }

func retireAndReport(current string, wait time.Duration) int {
	retired, left := retireUI(uiControlFor(), basePath(), current, wait)
	if len(left) > 0 {
		fmt.Println("候選窗程式（0c-ui.exe）正在使用中，沒有換成新版；閒置時會自動換新，不必登出。")
	}
	return retired
}

// restartUI：舊版 0c-ui 結束後請登入工作帶起新版（#243）。工作列選單只能經 0c-ui 執行命令，新裝的 DLL 會自己啟動它，
// 但還在執行中的應用程式載入的是舊 DLL，舊 DLL 連不上就什麼都不做，使用者要打一個字才恢復；沙箱宿主也啟動不了。
// 這裡可能是提權的安裝程式或低優先順序的自動更新工作，不直接啟動（理由同 startResidentEngine），一律交給登入工作。
// 測試替換用：go test 不執行這台機器上的登入工作。
var restartUI = func() {
	if taskExists(engineTaskName()) {
		_ = runEngineTask()
	}
}

// ensureUI（登入工作，一般權限、一般優先順序）：這個工作階段沒有 0c-ui 時啟動目前版本。
// 已經有一個就不動，還在忙的舊版由 handOverUI 下次處理；同時啟動時第二個會因單一實例自己退出。
func ensureUI() {
	current, _ := installedFromReceipt(receiptPath(), basePath(), "0c-ui.exe")
	if current == "" || elevatedOK() {
		return
	}
	if _, err := os.Stat(current); err != nil {
		return
	}
	if len(uiControlFor().list()) > 0 {
		return
	}
	if err := startUI(current); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update: 候選窗程式沒有啟動：", err)
	}
}

// startUI 脫離工作排程器的 job 啟動 0c-ui，工作結束時不帶走它（同 spawnEngine）。測試替換用。
var startUI = func(exe string) error {
	const flagsBase = windows.CREATE_NO_WINDOW | windows.NORMAL_PRIORITY_CLASS
	var err error
	for _, flags := range []uint32{flagsBase | createBreakawayFromJob, flagsBase} {
		cmd := exec.Command(exe)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		if err = cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	return err
}

// uiControlFor 測試替換用：go test 不關這台機器上真的候選窗程式（isolate_windows_test.go）。
var uiControlFor = windowsUIControl

func windowsUIControl() uiControl {
	return uiControl{
		list:   listUIWindows,
		idle:   inputIdle,
		close:  postClose,
		exited: waitProcessExit,
		sleep:  time.Sleep,
		now:    time.Now,
	}
}

func inputIdle() time.Duration {
	c := conditions()
	if !c.IdleKnown {
		return 0
	}
	return c.Idle
}

// uiWindows 列出本工作階段桌面上的候選窗視窗與所屬行程。FindWindowEx 只看得到自己桌面上的頂層視窗，
// 其他使用者工作階段的 0c-ui 不在其中，不會被關掉。
func uiWindows(visit func(h uintptr, pid uint32)) {
	class, _ := windows.UTF16PtrFromString(uiWindowClass)
	var prev uintptr
	for i := 0; i < 64; i++ {
		h, _, _ := procFindWindowExW.Call(0, prev, uintptr(unsafe.Pointer(class)), 0)
		if h == 0 {
			return
		}
		prev = h
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(windows.HWND(h), &pid); err == nil && pid != 0 {
			visit(h, pid)
		}
	}
}

// listUIWindows：每個行程一筆；任一個候選窗顯示著就算顯示中。
func listUIWindows() []uiProcess {
	index := map[uint32]int{}
	var out []uiProcess
	uiWindows(func(h uintptr, pid uint32) {
		visible := windows.IsWindowVisible(windows.HWND(h))
		if i, ok := index[pid]; ok {
			out[i].Visible = out[i].Visible || visible
			return
		}
		index[pid] = len(out)
		out = append(out, uiProcess{PID: pid, Path: processPath(pid), Visible: visible})
	})
	return out
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err = windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// postClose 對這個行程的候選窗送 WM_CLOSE。安裝程式以系統管理員執行時，送給一般權限的視窗不受 UIPI 限制。
func postClose(p uiProcess) error {
	sent := false
	var failed error
	uiWindows(func(h uintptr, pid uint32) {
		if pid != p.PID {
			return
		}
		if r, _, err := procPostMessageW.Call(h, wmClose, 0, 0); r == 0 {
			failed = err
			return
		}
		sent = true
	})
	if !sent {
		if failed == nil {
			failed = fmt.Errorf("找不到候選窗")
		}
		return failed
	}
	return nil
}

func waitProcessExit(p uiProcess, wait time.Duration) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.PID)
	if err != nil {
		return true // 已經結束
	}
	defer windows.CloseHandle(h)
	// PID 可能已被重用：路徑不同就是別的程式，原本的已經結束。
	if path := processPath(p.PID); path != "" && !strings.EqualFold(path, p.Path) {
		return true
	}
	ev, err := windows.WaitForSingleObject(h, uint32(wait/time.Millisecond))
	return err == nil && ev == windows.WAIT_OBJECT_0
}

// shellPendingAfterInstall：外殼裝好不等重新登入（#253）。TSF DLL 留在已經開著的應用程式裡照常打字，重新開啟才換成
// 新版；協調器接著核對收據與新引擎（ShellActiveWithoutRelogin）。
func shellPendingAfterInstall() bool { return false }
