//go:build darwin

package main

import (
	"encoding/json"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceNetworkGUIStatus(t *testing.T) {
	profile := os.Getenv("ZC_ACCEPTANCE_NETWORK_GUI_PROFILE")
	if profile == "" {
		t.Skip("supervised artificial GUI profile not provided")
	}
	if !strings.HasPrefix(profile, "/private/tmp/0c-") || filepath.Clean(profile) != profile || filepath.Base(profile) != "acceptance-profile" {
		t.Fatal("invalid artificial profile")
	}
	state, err := autoupdate.Load(profile)
	if err != nil || state.InstallID != "0c-artificial-acceptance" || state.Mode != autoupdate.ModeAsk || state.Stage != autoupdate.StagePrepared || state.Target == nil || state.Blocked == nil || state.Blocked.Reason != "needs_consent" {
		t.Fatal("not the completed artificial network probe", err)
	}
	out := map[string]any{"installed": "0.1.0-dev.40"}
	appendUpdateStateStatus(out, state, time.Now())
	if out["action"] != "finish" {
		t.Fatal("prepared GUI has no bounded install action")
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("acceptance_network_gui_status=" + string(data))
}
