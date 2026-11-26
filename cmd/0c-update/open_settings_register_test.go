//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

// #353：桌面掛著沒人回答的系統詢問時，0c-settings-launcher --register-entry 一直不結束，整個安裝停住。
func TestRegisterEntryGivesUpOnAHungLauncher(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "0c-settings-launcher")
	// 啟動器與它留下的子行程都拿著輸出不放，模擬卡在 LSRegisterURL 裡。
	if err := os.WriteFile(program, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := registerEntryTimeout
	registerEntryTimeout = 500 * time.Millisecond
	defer func() { registerEntryTimeout = saved }()
	start := time.Now()
	err := runRegisterEntry(program, filepath.Join(dir, "靈犀注音設定.app"))
	if err == nil || !strings.Contains(err.Error(), "沒有結束") {
		t.Fatalf("a hung launcher must be reported as a timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waited %v for a hung launcher", elapsed)
	}
}

func TestRegisterEntryPassesArguments(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "0c-settings-launcher")
	args := filepath.Join(dir, "args")
	if err := os.WriteFile(program, []byte("#!/bin/sh\necho \"$@\" > '"+args+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runRegisterEntry(program, "/Users/a b/Applications/靈犀注音設定.app"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(args)
	if got := strings.TrimSpace(string(b)); got != "--register-entry /Users/a b/Applications/靈犀注音設定.app" {
		t.Fatalf("args = %q", got)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\necho 'LSRegisterURL -10814' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runRegisterEntry(program, "/x"); err == nil || !strings.Contains(err.Error(), "-10814") {
		t.Fatalf("failure must carry the launcher output: %v", err)
	}
}

// 登記失敗留下記號、記進更新紀錄（連續失敗只記一筆），之後補登記成功時清掉記號並記下。
func TestRegisterSettingsEntryRetriesLater(t *testing.T) {
	base := t.TempDir()
	timeout := errors.New("登記設定程式入口超過 20s 沒有結束")
	events := func() []autoupdate.Event {
		s, err := autoupdate.Load(base)
		if err != nil {
			t.Fatal(err)
		}
		var out []autoupdate.Event
		for _, e := range s.History {
			if e.Kind == settingsEntryEvent {
				out = append(out, e)
			}
		}
		return out
	}
	if err := registerSettingsEntry(base, func() error { return nil }); err != nil || onDisk(settingsEntryPending(base)) || len(events()) != 0 {
		t.Fatalf("plain success: err=%v events=%v", err, events())
	}
	for i := 0; i < 3; i++ { // 安裝時逾時，之後的排程喚起（每 15 分鐘）也逾時
		if err := registerSettingsEntry(base, func() error { return timeout }); err != timeout {
			t.Fatal(err)
		}
	}
	if !onDisk(settingsEntryPending(base)) {
		t.Fatal("a failed registration must leave the marker for a later retry")
	}
	if e := events(); len(e) != 1 || e[0].Detail != timeout.Error() {
		t.Fatalf("history: %+v", e)
	}
	// 協調器存檔蓋掉了那一筆（安裝在協調器裡進行）：下一次又失敗時再記。
	if _, err := autoupdate.EditState(base, func(s *autoupdate.State) error { s.History = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	_ = registerSettingsEntry(base, func() error { return timeout })
	if e := events(); len(e) != 1 {
		t.Fatalf("history after overwrite: %+v", e)
	}
	if err := registerSettingsEntry(base, func() error { return nil }); err != nil || onDisk(settingsEntryPending(base)) {
		t.Fatalf("retry success: err=%v", err)
	}
	if e := events(); len(e) != 2 || e[1].Detail != settingsEntryRegistered {
		t.Fatalf("history after retry: %+v", e)
	}
	// 之後又失敗：再記一筆。
	_ = registerSettingsEntry(base, func() error { return timeout })
	if e := events(); len(e) != 3 {
		t.Fatalf("history after a new failure: %+v", e)
	}
}
