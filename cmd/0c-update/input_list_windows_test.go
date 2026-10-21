//go:build windows

package main

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// #191 發版前 code review：工作階段只給使用者名稱，SID 由名稱查回；查回的必須是 token 上的那個 SID，比對才成立。
func TestAccountSIDFromNameMatchesToken(t *testing.T) {
	process, err := processUser()
	if err != nil {
		t.Fatal(err)
	}
	if want, err := processUserSID(); err != nil || !strings.EqualFold(process.sid, want) {
		t.Fatalf("processUser 的 SID %q，token 是 %q（%v）", process.sid, want, err)
	}
	sid, name := process.sid, process.name
	if name == "" {
		t.Fatal("查不到這個程序的帳號名稱")
	}
	if got := accountSID(name); !strings.EqualFold(got, sid) {
		t.Fatalf("accountSID(%q) = %q，token 是 %q", name, got, sid)
	}
	if got := accountSID(`no-such-user-0c-191`); got != "" {
		t.Fatalf("不存在的帳號應查不到，得到 %q", got)
	}
	if !ownsDesktop(account{sid: sid}, account{sid: accountSID(name), name: name}, nil, true) {
		t.Fatal("同一位使用者要判成工作階段的使用者")
	}
}

// #191 第三輪 code review：工作階段使用者的 SID 取自桌面 shell 的擁有者（WTSEnumerateProcessesEx），不靠名稱。
// 查到的這個程序的 SID 要是 token 上的；有桌面 shell 時查得到它的擁有者，沒有（SSH、session 0）時為空，退回以名稱查。
func TestSessionProcessSIDMatchesToken(t *testing.T) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		t.Fatal(err)
	}
	process, err := processUser()
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionProcessSID(session, uint32(os.Getpid())); !strings.EqualFold(got, process.sid) {
		t.Fatalf("sessionProcessSID = %q，token 是 %q", got, process.sid)
	}
	if got := sessionProcessSID(session+1, uint32(os.Getpid())); got != "" {
		t.Fatalf("別的工作階段裡沒有這個程序，得到 %q", got)
	}
	shell := shellUserSID(session)
	if windows.GetShellWindow() == 0 {
		if shell != "" {
			t.Fatalf("沒有桌面 shell 卻查到 %q", shell)
		}
		t.Log("沒有桌面 shell（SSH、session 0）：不驗 shell 擁有者")
		return
	}
	if shell == "" {
		t.Fatal("有桌面 shell 卻查不到它的擁有者")
	}
	t.Logf("桌面 shell 的擁有者 %s，這個程序 %s", shell, process.sid)
}
