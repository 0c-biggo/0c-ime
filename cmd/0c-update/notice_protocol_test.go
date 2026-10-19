package main

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

func TestNoticeProtocolBindsBothActions(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, action := range []string{"finish", "snooze"} {
		raw, err := noticeActionURL(action, "0.1.0-dev.53", hash)
		if err != nil {
			t.Fatal(err)
		}
		got, args, err := noticeProtocolCommand(raw)
		want := []string{"-target", "0.1.0-dev.53", "-target-sha256", hash}
		if action == "finish" {
			want = append(want, "-notify")
		}
		if err != nil || got != action || !reflect.DeepEqual(args, want) {
			t.Fatalf("%s %v %v", got, args, err)
		}
	}
	valid, _ := noticeActionURL("finish", "0.1.0-dev.53", hash)
	for _, raw := range []string{"zc-update:finish", "zc-update:finish/", "https:finish?version=x", valid + "&version=0.1.0-dev.54", valid + "&extra=1", valid + "#fragment", strings.Replace(valid, "finish", "uninstall", 1), strings.Replace(valid, "zc-update:", "zc-update://", 1), strings.Replace(valid, hash, strings.ToUpper(hash), 1), valid + "&bad=%zz"} {
		if _, _, err := noticeProtocolCommand(raw); err == nil {
			t.Errorf("accepted unsafe or unbound URI %q", raw)
		}
	}
}

func TestNoticeProtocolSnoozePersistsAndRejectsReplacement(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Windows protocol; macOS uses the native notification delegate")
	}
	// Exercise the actual protocol -> CLI -> store path without a desktop or an install.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCALAPPDATA", home)
	base := basePath()
	s, err := autoupdate.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	s.Stage = autoupdate.StagePrepared
	s.Mode = autoupdate.ModeAsk
	s.Target = &autoupdate.Target{Version: "0.1.0-dev.53", SHA256: strings.Repeat("a", 64), Kind: "core"}
	if err = autoupdate.Save(base, s); err != nil {
		t.Fatal(err)
	}
	raw, _ := noticeActionURL("snooze", s.Target.Version, s.Target.SHA256)
	before := time.Now()
	if err = autoCommand("protocol", []string{raw}); err != nil {
		t.Fatal(err)
	}
	s, err = autoupdate.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if s.Snooze == nil || s.Snooze.Until.Before(before.Add(time.Hour)) || s.Snooze.Until.After(time.Now().Add(time.Hour)) || s.Mode != autoupdate.ModeAsk || s.Stage != autoupdate.StagePrepared {
		t.Fatalf("invalid snooze state: %+v", s)
	}
	s.Target.Version = "0.1.0-dev.54"
	if err = autoupdate.Save(base, s); err != nil {
		t.Fatal(err)
	}
	if err = autoCommand("protocol", []string{raw}); !errors.Is(err, autoupdate.ErrTargetChanged) {
		t.Fatalf("stale snooze: %v", err)
	}
	// The actual finish route must reject the old toast before doing any platform I/O.
	raw, _ = noticeActionURL("finish", "0.1.0-dev.53", strings.Repeat("a", 64))
	command, args, err := noticeProtocolCommand(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Omit only the outcome notification to avoid touching the test host's desktop.
	if err = autoCommand(command, args[:len(args)-1]); !errors.Is(err, autoupdate.ErrTargetChanged) {
		t.Fatalf("old toast consent accepted: %v", err)
	}
}
