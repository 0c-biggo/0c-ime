package main

import (
	"os"
	"path/filepath"
	"testing"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

// 登入單元擋在桌面啟動前：-login 不得查詢索引或下載，即使帶 -now；這些交給 0c-update.timer。
func TestAutoLoginDoesNotCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".local/share/zuin")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := editState(base, func(s *autoupdate.State) { s.Mode = autoupdate.ModeAuto; s.Feed = "https://127.0.0.1:1/feed.json" }); err != nil {
		t.Fatal(err)
	}

	if err := autoCommand("auto", []string{"-login", "-now"}); err != nil {
		t.Fatalf("auto -login: %v", err)
	}
	s, err := autoupdate.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if !s.LastCheck.IsZero() || s.CheckFailures != 0 {
		t.Fatalf("auto -login 不應查詢索引：LastCheck=%v CheckFailures=%d", s.LastCheck, s.CheckFailures)
	}
}
