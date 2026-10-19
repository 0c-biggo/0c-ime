package main

import (
	"testing"

	"git.biggo.com/0c/ime/internal/settings"
)

// #117：中英切換鍵取代「左 Shift 切換中英」。舊檔的 left_shift=true 讀成「左右 Shift」；設定契約回報引擎實際載入的值。
func TestToggleKeySettingContractReportsPendingAndUnknownRuntime(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"left_shift":true}`)
	for _, tc := range []struct{ runtime, want string }{{"both", "both"}, {"right", "right"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"toggle_key": tc.runtime})["toggle-key"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"toggle_key": tc.runtime}, true))
		it := item(t, doc, "toggle-key")
		if it.Value != "both" || it.Pending != (tc.runtime == "right") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"toggle-key", "left", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
	for _, it := range settings.Default().Items() {
		if it.Key == "toggle-key" {
			if it.Value != "right" || it.Default != "right" || it.ValueLabels["both"] != "左右 Shift" {
				t.Fatalf("UI contract: %+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到中英切換鍵")
}
