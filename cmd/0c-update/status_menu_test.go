package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.biggo.com/0c/ime/internal/autoupdate"
)

func TestStatusJSONIncludesBoundMenuAssetAndSnooze(t *testing.T) {
	now := time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)
	s := &autoupdate.State{Mode: autoupdate.ModeAsk, Stage: autoupdate.StagePrepared,
		Target:  &autoupdate.Target{Version: "0.1.0-dev.41", Kind: "shell", SHA256: strings.Repeat("a", 64)},
		Blocked: &autoupdate.Blocked{Reason: "needs_consent"}}
	s.Snooze = &autoupdate.NoticeSnooze{Version: s.Target.Version, SHA256: s.Target.SHA256, Until: now.Add(time.Hour)}
	if profile := os.Getenv("ZC_ACCEPTANCE_STATUS_PROFILE"); profile != "" {
		if !strings.HasPrefix(profile, "/private/tmp/0c-") || filepath.Clean(profile) != profile || filepath.Base(profile) != "acceptance-profile" {
			t.Fatal("invalid artificial acceptance profile")
		}
		loaded, err := autoupdate.Load(profile)
		if err != nil || loaded.InstallID != "0c-artificial-acceptance" || loaded.Mode != autoupdate.ModeAsk || loaded.Snooze == nil {
			t.Fatal("not the prepared artificial GUI snooze state", err)
		}
		s, now = loaded, time.Now()
	}
	out := map[string]any{"installed": "0.1.0-dev.40"}
	appendUpdateStateStatus(out, s, now)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Target       struct{ Version, Kind, SHA256 string }
		Action       string
		SnoozedUntil time.Time `json:"notice_snoozed_until"`
	}
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Target.SHA256 != s.Target.SHA256 || decoded.Target.Version != s.Target.Version || decoded.Action != "finish" || !decoded.SnoozedUntil.Equal(s.Snooze.Until) {
		t.Fatal("real status mapping cannot produce the asset-bound presenter action", string(data))
	}
	t.Log("acceptance_status_json=" + string(data))
	s.Snooze.SHA256 = strings.Repeat("b", 64)
	out = map[string]any{}
	appendUpdateStateStatus(out, s, now)
	if _, ok := out["notice_snoozed_until"]; ok {
		t.Fatal("stale asset snooze shown on replacement target")
	}
	s.Target, s.Blocked = nil, nil
	out = map[string]any{}
	appendUpdateStateStatus(out, s, now)
	if out["target"] != nil || out["action"] != nil {
		t.Fatal("idle state exposed an install command")
	}
}
