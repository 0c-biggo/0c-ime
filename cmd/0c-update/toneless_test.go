package main

import "testing"

// #150：「不打聲調也能選字」經 settings --json 給設定程式；引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
// 開著時中英混打標成不起作用（與混打二選一），值照設定檔。
func TestTonelessContractReportsPendingAndDisablesMixedInput(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"toneless":true,"mixed_input":true}`)
	for _, tc := range []struct{ runtime, want string }{{"false", "off"}, {"true", "on"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"toneless": tc.runtime})["toneless"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"toneless": tc.runtime}, true))
		it := item(t, doc, "toneless")
		if it.Value != "on" || it.Pending != (tc.runtime == "false") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
		if mixed := item(t, doc, "mixed-input"); mixed.Value != "on" || !mixed.Disabled || mixed.DisabledReason != "不打聲調時關閉" {
			t.Fatalf("混打反灰：%+v", mixed)
		}
	}
	if o, err := parseSetArgs([]string{"toneless", "on", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
