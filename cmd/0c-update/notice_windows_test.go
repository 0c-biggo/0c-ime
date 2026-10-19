//go:build windows

package main

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestToastXMLCarriesBoundInstallAndSnooze(t *testing.T) {
	hash := strings.Repeat("a", 64)
	finish, _ := noticeActionURL("finish", "0.1.0-dev.53", hash)
	snooze, _ := noticeActionURL("snooze", "0.1.0-dev.53", hash)
	// Execute the production XML builder with Windows Runtime, stopping before
	// creating/showing a notification. No desktop or registry changes.
	script, _, ok := strings.Cut(toastScript, "$n = ")
	if !ok {
		t.Fatal("missing Toast creation boundary")
	}
	script = "$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)\n" + script + "[Console]::WriteLine($doc.GetXml())"
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePS(script))
	cmd.Env = append(os.Environ(), "ZC_TOAST_TITLE=Test <title>", "ZC_TOAST_BODY=Test & body", "ZC_TOAST_ACTION="+finish, "ZC_TOAST_SNOOZE="+snooze)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("XML builder: %v %s", err, out)
	}
	var doc struct {
		Texts   []string `xml:"visual>binding>text"`
		Actions []struct {
			Type string `xml:"activationType,attr"`
			URI  string `xml:"arguments,attr"`
		} `xml:"actions>action"`
	}
	if err = xml.Unmarshal(out, &doc); err != nil {
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
