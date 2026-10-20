package main

import (
	"runtime"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #173：「Caps Lock 開著時直接打英文」經 settings --json 給設定程式；引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
func TestCapsLockEnglishContractReportsPendingAndUnknownRuntime(t *testing.T) {
	if !settings.CapsLockEnglishSetting(runtime.GOOS) {
		t.Skip("macOS 沒有這一項")
	}
	base := t.TempDir()
	writeSettings(t, base, `{"caps_lock_english":true}`)
	for _, tc := range []struct{ runtime, want string }{{"false", "off"}, {"true", "on"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"caps_lock_english": tc.runtime})["caps-lock-english"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"caps_lock_english": tc.runtime}, true))
		it := item(t, doc, "caps-lock-english")
		if it.Value != "on" || it.Pending != (tc.runtime == "false") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"caps-lock-english", "on", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
