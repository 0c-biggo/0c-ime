package main

import (
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #133：「選字後游標移到下一個字」經 settings --json 給設定程式；引擎載入的值不同時標示待套用。
func TestAdvanceAfterSelectContractReportsPendingAndUnknownRuntime(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"advance_after_select":true}`)
	for _, tc := range []struct{ runtime, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"advance_after_select": tc.runtime})["advance-after-select"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"advance_after_select": tc.runtime}, true))
		it := item(t, doc, "advance-after-select")
		if it.Value != "on" || it.Pending != (tc.runtime == "false") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"advance-after-select", "on", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
	for _, it := range settings.Default().Items() {
		if it.Key == "advance-after-select" {
			if it.Value != "off" || it.Default != "off" || it.Label != "選字後游標移到下一個字" || it.Description == "" {
				t.Fatalf("UI contract: %+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到選字後游標移到下一個字的開關")
}
