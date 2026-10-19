//go:build linux

package main

// 更新器與 IBus 外殼的交接入口：與 Fcitx5 外殼（update_guard.h）相同的 session bus 名稱與介面，
// 只傳狀態，不傳組字文字。更新器據此確認沒有組字、使用者閒置後才讓外殼退出並替換檔案。

import (
	"encoding/json"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	guardName      = "org.biggo.ime.Update"
	guardPath      = dbus.ObjectPath("/update")
	guardInterface = "org.biggo.ime.Update1"
	// exitDelay 讓 ExitForUpdate 的回覆先送出再結束行程。
	exitDelay = 200 * time.Millisecond
)

// exitProcess 結束外殼；測試時換掉。
var exitProcess = func() { os.Exit(0) }

type guardStatus struct {
	Busy             bool   `json:"busy"`
	Idle             int64  `json:"idle_seconds"`
	Version          string `json:"version"`
	Program          string `json:"program"`
	RestoreSupported bool   `json:"restore_supported"`
	Frontend         string `json:"frontend"`
	// Active：靈犀注音是目前選用的 IBus engine；更新後由更新器以 SetGlobalEngine 重新叫起外殼。
	Active      bool   `json:"active"`
	IBusAddress string `json:"ibus_address"`
}

type guard struct{ s *service }

// busy 在鎖內呼叫：任一欄位還有組字或候選。
func (s *service) busy() bool {
	for _, e := range s.engines {
		if e.preedit != "" || len(e.table) > 0 || len(e.session.log) > 0 || e.session.recovered != nil {
			return true
		}
	}
	return false
}

func (s *service) status() guardStatus {
	return guardStatus{Busy: s.busy(), Idle: int64(time.Since(s.lastKey) / time.Second), Version: buildVersion,
		Frontend: "ibus", Active: len(s.engines) > 0, IBusAddress: s.address}
}

func (g guard) Status() (string, *dbus.Error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	b, _ := json.Marshal(g.s.status())
	return string(b), nil
}

// ExitForUpdate 在沒有組字且閒置夠久時結束外殼。檢查與結束之間一直持有鎖，不會插入新的按鍵。
func (g guard) ExitForUpdate(idleNeeded uint64) (string, *dbus.Error) {
	g.s.mu.Lock()
	st := g.s.status()
	if st.Busy {
		g.s.mu.Unlock()
		return "composing", nil
	}
	if uint64(max(st.Idle, 0)) < idleNeeded {
		g.s.mu.Unlock()
		return "typing", nil
	}
	// 不再釋放鎖：之後的 IBus 呼叫都停在這裡，直到行程結束。0c-server 的 session 隨連線關閉。
	time.AfterFunc(exitDelay, exitProcess)
	return "ready", nil
}

// RestoreInputMethod 是 Fcitx5 外殼的介面；IBus 由更新器以 SetGlobalEngine 恢復，這裡不做事。
func (g guard) RestoreInputMethod(string, string) *dbus.Error { return nil }

// startGuard 在 session bus 上提供交接入口；拿不到名稱（例如 Fcitx5 外殼同時在執行）時不提供，
// 更新器就不會自動重啟這個外殼。
func (s *service) startGuard() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	reply, err := conn.RequestName(guardName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return
	}
	conn.Export(guard{s}, guardPath, guardInterface)
}
