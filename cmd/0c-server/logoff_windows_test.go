//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// 登出／關機時存檔靠的是主控台控制事件：TSF DLL 以 CREATE_NO_WINDOW|DETACHED_PROCESS 啟動引擎，
// 引擎沒有主控台也沒有視窗，系統仍會對它登記的控制處理常式送 CTRL_LOGOFF_EVENT／CTRL_SHUTDOWN_EVENT，
// Go 執行階段轉成 SIGTERM，main 存檔後結束（2026-10-15 在 Windows 11 25H2 登出實測，#129）。
// 但行程一旦載入 user32.dll 或 gdi32.dll，系統就不再呼叫這兩個事件的處理常式（SetConsoleCtrlHandler 文件），
// 登出時會直接被終止、遺失最後一分鐘內的學習。引擎若真的需要視窗 API，要改成以隱藏的頂層視窗處理
// WM_QUERYENDSESSION 存檔，再拿掉這個測試。
func TestEngineDoesNotLoadUser32(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "0c-server.exe")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	dir := t.TempDir()
	data, _ := filepath.Abs("../../testdata/toy")
	pipe := fmt.Sprintf(`\\.\pipe\0c-test-modules-%d`, os.Getpid())
	server := exec.Command(binary, "-data", data, "-user", filepath.Join(dir, "user.json"), "-socket", pipe)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	timeout := 200 * time.Millisecond
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if c, err := winio.DialPipe(pipe, &timeout); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("引擎沒有開啟 named pipe")
		}
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE, uint32(server.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ModuleEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var modules []string
	for err = windows.Module32First(snapshot, &entry); err == nil; err = windows.Module32Next(snapshot, &entry) {
		modules = append(modules, strings.ToLower(windows.UTF16ToString(entry.Module[:])))
	}
	if len(modules) == 0 {
		t.Fatal("讀不到引擎載入的模組")
	}
	for _, name := range modules {
		if name == "user32.dll" || name == "gdi32.dll" {
			t.Fatalf("引擎載入了 %s，登出時收不到 CTRL_LOGOFF_EVENT、來不及存檔；模組：%v", name, modules)
		}
	}
}
