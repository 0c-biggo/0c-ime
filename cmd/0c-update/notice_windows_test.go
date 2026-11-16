//go:build windows

package main

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/winshell"
)

// 通知的兩個按鈕帶著同一個資產（版本＋SHA-256），按下時 0c-update 能照網址核對。
// 內容由 winshell.ToastXML 組出，在行程內交給 WinRT，不經 PowerShell（#289）。
func TestToastXMLCarriesBoundInstallAndSnooze(t *testing.T) {
	hash := strings.Repeat("a", 64)
	finish, _ := noticeActionURL("finish", "0.1.0-dev.53", hash)
	snooze, _ := noticeActionURL("snooze", "0.1.0-dev.53", hash)
	out := winshell.ToastXML(winshell.Toast{Title: "Test <title>", Body: "Test & body", Action: finish, Snooze: snooze})
	var doc struct {
		Texts   []string `xml:"visual>binding>text"`
		Actions []struct {
			Type string `xml:"activationType,attr"`
			URI  string `xml:"arguments,attr"`
		} `xml:"actions>action"`
	}
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Texts) != 2 || doc.Texts[0] != "Test <title>" || doc.Texts[1] != "Test & body" || len(doc.Actions) != 2 {
		t.Fatalf("bad XML: %s", out)
	}
	for i, want := range []string{finish, snooze} {
		if doc.Actions[i].Type != "protocol" || doc.Actions[i].URI != want {
			t.Fatalf("button lost asset identity: %+v", doc.Actions[i])
		}
		if _, _, err := noticeProtocolCommand(doc.Actions[i].URI); err != nil {
			t.Fatal(err)
		}
	}
}
