package main

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/bridge/ipc"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
)

func modelStatusDial(info map[string]string, ok bool) autoupdate.Dialer {
	return func(time.Duration) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var request ipc.Envelope
			if ipc.ReadFrame(server, &request) == nil {
				ipc.WriteFrame(server, ipc.Reply{OK: ok && request.Op == ipc.OpHealth, Info: info})
			}
		}()
		return client, nil
	}
}

func TestModelStatusReportsFallbackWithoutInferringFromOldStartupFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		info map[string]string
		want string
	}{
		{"failed workers", map[string]string{"version": "dev", "selection_state": "fallback", "punctuation_state": "fallback", "neural_startup_state": "enabled", "punctuation": "true"}, "fallback"},
		{"old engine", map[string]string{"version": "old", "neural_startup_state": "enabled", "punctuation": "true"}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := modelStatus(modelStatusDial(tc.info, true))
			if got["state"] != "running" || got["selection_state"] != tc.want || got["punctuation_state"] != tc.want {
				t.Fatalf("runtime status: %v", got)
			}
			var text bytes.Buffer
			printModelStatus(&text, got)
			if strings.Contains(text.String(), "已就緒") {
				t.Fatalf("unavailable worker presented as ready: %s", text.String())
			}
			if tc.want == "fallback" && (!strings.Contains(text.String(), "已退回統計引擎") || !strings.Contains(text.String(), "已退回原文提交")) {
				t.Fatalf("fallback not explained: %s", text.String())
			}
		})
	}
}

func TestModelStatusDistinguishesStoppedEngineAndFailedQuery(t *testing.T) {
	missing := func(time.Duration) (net.Conn, error) { return nil, errors.New("no engine") }
	if got := modelStatus(missing); got["state"] != "not_running" {
		t.Fatalf("stopped engine: %v", got)
	}
	if got := modelStatus(modelStatusDial(nil, false)); got["state"] != "unavailable" {
		t.Fatalf("query failure must not imply stopped engine: %v", got)
	}
}

func TestConfigureFlagsAcceptsMenuFeedbackAndRejectsUnexpectedArguments(t *testing.T) {
	apply, notice, err := configureFlags([]string{"-apply", "-notify"})
	if err != nil || !apply || !notice {
		t.Fatalf("menu flags: %v %v %v", apply, notice, err)
	}
	for _, args := range [][]string{{"-apply", "-apply"}, {"-notify", "-notify"}, {"-other"}, {"on"}} {
		if _, _, err := configureFlags(args); err == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
}

func TestModelStatusExplainsFallbackReason(t *testing.T) {
	got := modelStatus(modelStatusDial(map[string]string{"version": "dev", "selection_state": "fallback", "selection_reason": "missing_runtime",
		"punctuation_state": "disabled_by_setting"}, true))
	if got["selection_reason"] != "missing_runtime" || got["punctuation_reason"] != nil {
		t.Fatalf("runtime status: %v", got)
	}
	var text bytes.Buffer
	printModelStatus(&text, got)
	if !strings.Contains(text.String(), "已退回統計引擎（缺少系統元件：Visual C++ 執行階段）") {
		t.Fatalf("missing runtime not explained: %s", text.String())
	}
}

// #181：系統安裝找到模型卻缺 numpy／onnxruntime，或模型放在個人目錄時，狀態要說清楚。
func TestModelStatusExplainsRuntimeMissingAndUserModel(t *testing.T) {
	got := modelStatus(modelStatusDial(map[string]string{"version": "dev", "selection_state": "runtime_missing",
		"punctuation_state": "ready", "punctuation_source": "user"}, true))
	if got["selection_state"] != "runtime_missing" || got["punctuation_source"] != "user" || got["selection_source"] != nil {
		t.Fatalf("runtime status: %v", got)
	}
	var text bytes.Buffer
	printModelStatus(&text, got)
	for _, want := range []string{"神經選字：缺少推論元件（請安裝 python3-numpy、python3-onnxruntime），使用統計引擎", "補標點：已就緒（個人目錄的模型）"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("missing %q: %s", want, text.String())
		}
	}
}
