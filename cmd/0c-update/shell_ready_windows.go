//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wtsapi32                   = windows.NewLazySystemDLL("wtsapi32.dll")
	wtsQuerySessionInformation = wtsapi32.NewProc("WTSQuerySessionInformationW")
	wtsFreeMemory              = wtsapi32.NewProc("WTSFreeMemory")
)

// WTSINFOW 的 WTSSessionInfo (24)；WTSLogonTime (18) 並不受支援。
// WCHAR 陣列後有 LARGE_INTEGER 的 8-byte alignment，亦適用 Windows amd64/arm64。
type wtsSessionInfo struct {
	State, SessionID                                                   uint32
	IncomingBytes, OutgoingBytes, IncomingFrames, OutgoingFrames       uint32
	IncomingCompressedBytes, OutgoingCompressedBytes                   uint32
	WinStationName                                                     [32]uint16
	Domain                                                             [17]uint16
	UserName                                                           [21]uint16
	_                                                                  uint32
	ConnectTime, DisconnectTime, LastInputTime, LogonTime, CurrentTime int64
}

func desktopLogonTime() (time.Time, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return time.Time{}, err
	}
	if session == 0 {
		return time.Time{}, fmt.Errorf("不在互動式工作階段")
	}
	var buffer *wtsSessionInfo
	var size uint32
	r, _, err := wtsQuerySessionInformation.Call(0, uintptr(session), 24,
		uintptr(unsafe.Pointer(&buffer)), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return time.Time{}, fmt.Errorf("查詢工作階段：%w", err)
	}
	defer wtsFreeMemory.Call(uintptr(unsafe.Pointer(buffer)))
	if buffer == nil || uintptr(size) < unsafe.Sizeof(wtsSessionInfo{}) {
		return time.Time{}, fmt.Errorf("登入資訊不完整")
	}
	info := buffer
	if info.SessionID != session || info.UserName[0] == 0 || info.LogonTime <= 0 {
		return time.Time{}, fmt.Errorf("沒有有效的登入時間")
	}
	ft := windows.Filetime{LowDateTime: uint32(info.LogonTime), HighDateTime: uint32(uint64(info.LogonTime) >> 32)}
	return time.Unix(0, ft.Nanoseconds()), nil
}

func shellReadyAfterInstall(receipt, version string, logon time.Time) (bool, error) {
	b, err := os.ReadFile(receipt)
	if err != nil {
		return false, err
	}
	var r struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return false, err
	}
	if r.Version != version {
		return false, fmt.Errorf("外殼收據版本 %q，預期 %s", r.Version, version)
	}
	stat, err := os.Stat(receipt)
	if err != nil {
		return false, err
	}
	// 收據在註冊完成後才寫入。斷線重連、重啟 Explorer 或 UAC 都不會改變 WTS 登入時間。
	return !logon.IsZero() && logon.After(stat.ModTime()), nil
}

func (p *system) ShellReady(_ context.Context, version string) (bool, error) {
	logon, err := desktopLogonTime()
	if err != nil {
		return false, err
	}
	return shellReadyAfterInstall(filepath.Join(p.base, "install-receipt.json"), version, logon)
}
