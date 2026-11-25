package main

import "testing"

// 「中文與數字之間自動加空格」經 settings --json 給設定程式；引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
func TestCJKDigitSpaceContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"cjk_digit_space":true}`)
	for _, tc := range []struct{ runtime, want string }{{"false", "off"}, {"true", "on"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"cjk_digit_space": tc.runtime})["cjk-digit-space"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"cjk_digit_space": tc.runtime}, true))
		it := item(t, doc, "cjk-digit-space")
		if it.Value != "on" || it.Default != "off" || it.Locked || it.Pending != (tc.runtime == "false") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
}
