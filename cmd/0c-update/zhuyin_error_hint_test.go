package main

import "testing"

// #300：「打錯注音時提示」經 settings --json 給設定程式；預設開，引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
// 不屬於輸入習慣風格，新安裝（內建靈犀注音風格）也不鎖。
func TestZhuyinErrorHintContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"zhuyin_error_hint":false}`)
	for _, tc := range []struct{ runtime, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"zhuyin_error_hint": tc.runtime})["zhuyin-error-hint"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"zhuyin_error_hint": tc.runtime}, true))
		it := item(t, doc, "zhuyin-error-hint")
		if it.Value != "off" || it.Default != "on" || it.Locked || it.Pending != (tc.runtime == "true") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"zhuyin-error-hint", "off", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
