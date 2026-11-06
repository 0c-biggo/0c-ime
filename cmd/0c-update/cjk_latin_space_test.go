package main

import "testing"

// #273：「中英文之間自動加空格」經 settings --json 給設定程式；引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
// 不屬於輸入習慣風格，新安裝（內建靈犀注音風格）也不鎖。
func TestCJKLatinSpaceContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"cjk_latin_space":true}`)
	for _, tc := range []struct{ runtime, want string }{{"false", "off"}, {"true", "on"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"cjk_latin_space": tc.runtime})["cjk-latin-space"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"cjk_latin_space": tc.runtime}, true))
		it := item(t, doc, "cjk-latin-space")
		if it.Value != "on" || it.Default != "off" || it.Locked || it.Pending != (tc.runtime == "false") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"cjk-latin-space", "on", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
