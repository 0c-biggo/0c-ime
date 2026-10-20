package main

import (
	"path/filepath"
	"strings"
	"time"
)

// 候選窗程式 0c-ui 的交接（Windows）：0c-ui 由 TSF DLL 在需要時啟動、常駐到登出，升級時不會自己換新版。
// #159 的全螢幕檢查寫在 0c-ui，舊的那一份不退出，升級後要重新登入才生效。安裝、回復、解除安裝，以及之後
// 每次自動更新排程與登入工作喚起時，請本工作階段裡不是目前版本的 0c-ui 在沒有顯示任何東西、使用者也停手時
// 結束；新版不由這裡啟動，照原本的機制由 DLL 在下一次換焦點或要畫候選窗時啟動（版本目錄依安裝收據）。
// 0.2.0 以前的 0c-ui 沒有退出指令，以 WM_CLOSE 關它的視窗（視窗程序交給 DefWindowProc → WM_DESTROY →
// 結束訊息迴圈）；新版的 0c-ui 收到 WM_CLOSE 時自己再等到沒有顯示、沒有新訊息才結束。

// uiProcess 是一個執行中的候選窗程式：視窗所屬的行程與它的執行檔。
type uiProcess struct {
	PID     uint32
	Path    string
	Visible bool // 候選窗或提示正顯示著
}

const (
	// uiQuiet：使用者停手（沒有鍵盤、滑鼠輸入）多久之後才請舊 0c-ui 結束。DLL 送給 0c-ui 的訊息都來自按鍵，
	// 停手這麼久，就不會有顯示訊息正在路上、一關掉就讓剛出現的候選窗消失。
	uiQuiet = time.Second
	// uiExitWait：送出 WM_CLOSE 之後等行程結束多久；新版 0c-ui 會等到自己閒下來，沒等到就留給下一次。
	uiExitWait = 3 * time.Second
	uiPoll     = 200 * time.Millisecond
)

// underDir：path 在 dir 之下（不分大小寫，Windows 路徑）。
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	p, d := strings.ToLower(filepath.Clean(path)), strings.ToLower(filepath.Clean(dir))
	return strings.HasPrefix(p, d+string(filepath.Separator))
}

// outdatedUI 回傳要退出的候選窗程式：執行檔在這個安裝的個人資料目錄（base）之下、又不是目前版本（current）
// 的那一份。其他帳號自己的安裝、或不認得的程式都不碰。current 為空（解除安裝）時 base 底下的全部都要退出。
func outdatedUI(running []uiProcess, base, current string) []uiProcess {
	var out []uiProcess
	for _, p := range running {
		if !underDir(p.Path, base) {
			continue
		}
		if current != "" && strings.EqualFold(filepath.Clean(p.Path), filepath.Clean(current)) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// uiControl 是交接用到的系統操作；測試以假的取代。
type uiControl struct {
	list   func() []uiProcess                  // 本工作階段裡執行中的候選窗程式
	idle   func() time.Duration                // 使用者多久沒有輸入
	close  func(uiProcess) error               // 請它結束（WM_CLOSE）
	exited func(uiProcess, time.Duration) bool // 等它結束，最多等這麼久
	sleep  func(time.Duration)
	now    func() time.Time
	quiet  time.Duration // 停手多久才關；零值為 uiQuiet（macOS 依情況另訂，見 shell_handover_darwin.go）
}

// retireUI 在 wait 之內請過期的候選窗程式結束：只在它沒有顯示任何東西、使用者停手 uiQuiet 之後才關。
// 回傳結束了幾個，以及到期時仍在執行的（使用者一直在打字或選字；下一次排程喚起再試，或登出時結束）。
func retireUI(c uiControl, base, current string, wait time.Duration) (retired int, left []uiProcess) {
	return retireProcesses(c, func() []uiProcess { return outdatedUI(c.list(), base, current) }, wait)
}

// retireProcesses 是交接的共同迴圈：pending 回傳目前還要結束的程式（Windows 的 0c-ui、macOS 的 0c.app）。
func retireProcesses(c uiControl, pending func() []uiProcess, wait time.Duration) (retired int, left []uiProcess) {
	deadline := c.now().Add(wait)
	for {
		todo := pending()
		if len(todo) == 0 {
			return retired, nil
		}
		quiet := c.quiet
		if quiet == 0 {
			quiet = uiQuiet
		}
		for _, p := range todo {
			if p.Visible || c.idle() < quiet {
				continue
			}
			if c.close(p) != nil {
				continue
			}
			if c.exited(p, uiExitWait) {
				retired++
			}
		}
		if !c.now().Before(deadline) {
			return retired, pending()
		}
		c.sleep(uiPoll)
	}
}
