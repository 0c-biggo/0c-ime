//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func processUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// 在讀寫更新狀態、註冊 TSF 或接觸引擎之前核對 UAC 交接。
// 不把原使用者的 LOCALAPPDATA 覆寫到另一個管理員的環境中。
func validateElevationOrigin(action, sid, base string) error {
	if sid == "" && base == "" {
		return nil
	}
	if action != "finish" || sid == "" || !filepath.IsAbs(base) {
		return fmt.Errorf("無效的 UAC 更新交接")
	}
	current, err := processUserSID()
	if err != nil {
		return err
	}
	if !strings.EqualFold(current, sid) {
		return fmt.Errorf("UAC 使用了不同帳號，更新未完成；請以原桌面使用者的同一帳號確認權限，原版與待辦保留")
	}
	if !strings.EqualFold(filepath.Clean(base), filepath.Clean(basePath())) {
		return fmt.Errorf("UAC 前後安裝目錄不同，更新未完成；原版與待辦保留")
	}
	if !elevatedOK() {
		return fmt.Errorf("UAC 更新交接未取得系統管理員權限")
	}
	return nil
}

// SHELLEXECUTEINFOW：SEE_MASK_NOCLOSEPROCESS 取得子程序結果，
// SEE_MASK_NOASYNC 確保啟動完成，SEE_MASK_FLAG_NO_UI 不彈出額外錯誤視窗。
// https://learn.microsoft.com/windows/win32/api/shellapi/ns-shellapi-shellexecuteinfow
type shellExecuteInfo struct {
	size, mask                        uint32
	window                            windows.Handle
	verb, file, parameters, directory *uint16
	show                              int32
	instance, idList                  uintptr
	class                             *uint16
	classKey                          windows.Handle
	hotKey                            uint32
	icon                              windows.Handle
	process                           windows.Handle
}

var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func launchElevated(file string, args []string) (windows.Handle, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return 0, err
	}
	exe, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return 0, err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return 0, err
	}
	info := shellExecuteInfo{mask: 0x40 | 0x100 | 0x400, verb: verb, file: exe, parameters: params, show: windows.SW_HIDE}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, err := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return 0, err
	}
	if info.process == 0 {
		return 0, fmt.Errorf("無法核對 UAC 更新程序")
	}
	return info.process, nil
}

// 不把 UAC 啟動成功當成安裝完成；原使用者的程序等候結果並回報通知。
func relaunchElevated(args []string) error {
	return relaunchElevatedWith(args, launchElevated)
}

func relaunchElevatedWith(args []string, launch func(string, []string) (windows.Handle, error)) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	sid, err := processUserSID()
	if err != nil {
		return err
	}
	args = append(append([]string{}, args...), "-origin-user", sid, "-origin-base", basePath())
	process, err := launch(self, args)
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return fmt.Errorf("已取消系統管理員確認；原版與更新待辦保留：%w", err)
	}
	if err != nil {
		return fmt.Errorf("需要系統管理員確認：%w", err)
	}
	defer windows.CloseHandle(process)
	deadline := time.Now().Add(35 * time.Minute) // 更新程序本身有 30 分鐘上限。
	for time.Now().Before(deadline) {
		result, err := windows.WaitForSingleObject(process, 1000)
		if err != nil {
			return err
		}
		if result == windows.WAIT_OBJECT_0 {
			var code uint32
			if err := windows.GetExitCodeProcess(process, &code); err != nil {
				return err
			}
			if code != 0 {
				return fmt.Errorf("權限確認後更新未完成（結束碼 %d）；請確認使用原桌面使用者的同一帳號，並從選單重試", code)
			}
			return nil
		}
	}
	return fmt.Errorf("尚未取得 UAC 更新結果，請從選單檢查更新狀態")
}
