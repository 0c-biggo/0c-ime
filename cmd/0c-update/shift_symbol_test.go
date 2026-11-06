package main

import "testing"

// #258：「Shift＋符號打半形」經 settings --json 給設定程式；引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
// 不屬於輸入習慣風格，新安裝（內建靈犀注音風格）也不鎖。
func TestShiftSymbolHalfwidthContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"shift_symbol_halfwidth":true}`)
	for _, tc := range []struct{ runtime, want string }{{"false", "off"}, {"true", "on"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"shift_symbol_halfwidth": tc.runtime})["shift-symbol-halfwidth"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"shift_symbol_halfwidth": tc.runtime}, true))
		it := item(t, doc, "shift-symbol-halfwidth")
		if it.Value != "on" || it.Default != "off" || it.Locked || it.Pending != (tc.runtime == "false") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"shift-symbol-halfwidth", "on", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
