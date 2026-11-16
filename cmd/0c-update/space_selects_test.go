package main

import (
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #107、#110：「空白鍵選字」經 settings --json 給設定程式；引擎載入的值不同時標示待套用。
func TestSpaceSelectsSettingContractReportsPendingAndUnknownRuntime(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"space_selects":false}`)
	for _, tc := range []struct{ runtime, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"space_selects": tc.runtime})["space-selects"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"space_selects": tc.runtime}, true))
		it := item(t, doc, "space-selects")
		if it.Value != "off" || it.Pending != (tc.runtime == "true") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"space-selects", "off", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
	for _, it := range settings.Default().Items() {
		if it.Key == "space-selects" {
			if it.Value != "on" || it.Default != "on" || it.Label != "空白鍵選字" || it.Description == "" {
				t.Fatalf("UI contract: %+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到空白鍵選字開關")
}

// #288：中英混打開著時，空白鍵選字標為停用、指向混打；引擎照設定檔回報 space_selects，不能因停用變成「待套用」。
func TestSpaceSelectsContractDisabledWhileMixedInput(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"mixed_input":true,"space_selects":true}`)
	doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"space_selects": "true", "mixed_input": "true"}, true))
	it := item(t, doc, "space-selects")
	if !it.Disabled || it.DisabledBy != "mixed-input" || it.DisabledReason == "" || it.Value != "on" || it.Effective != "" || it.Pending {
		t.Fatalf("混打時的空白鍵選字：%+v", it)
	}
	writeSettings(t, base, `{"mixed_input":false,"space_selects":true}`)
	if it := item(t, buildSettingsDoc(base, modelStatusDial(nil, false)), "space-selects"); it.Disabled || it.DisabledBy != "" {
		t.Fatalf("關掉混打後恢復：%+v", it)
	}
}
