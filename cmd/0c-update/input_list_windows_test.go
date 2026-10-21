//go:build windows

package main

import (
	"strings"
	"testing"
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
