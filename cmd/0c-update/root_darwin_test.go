//go:build darwin

package main

import (
	"os"
	"testing"
)

func TestMacAgentLabelMatchesSchedule(t *testing.T) {
	if macAgentLabel != agentLabel {
		t.Fatalf("修復路徑的排程標籤 %q 與 agentLabel %q 不同", macAgentLabel, agentLabel)
	}
}

// 一般帳號執行時 dropRoot 什麼都不做（測試不以 root 執行）。
func TestDropRootIsNoOpForUsers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 執行")
	}
	if err := dropRoot([]string{"status"}); err != nil {
		t.Fatal(err)
	}
}

// 這台機器的帳號資料庫：自己的帳號查得到，家目錄是絕對路徑。
func TestLookupAccountSelf(t *testing.T) {
	a, err := lookupAccount(os.Getuid())
	if err != nil || a.UID != os.Getuid() || a.Home == "" || a.Home[0] != '/' {
		t.Fatalf("%+v %v", a, err)
	}
}
