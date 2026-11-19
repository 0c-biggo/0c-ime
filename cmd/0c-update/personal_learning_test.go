package main

import "testing"

// #297：「記住我選的字詞」經 settings --json 給設定程式；預設開，引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
// 個人偏好，不屬於輸入習慣風格，新安裝（內建靈犀注音風格）也不鎖。
func TestPersonalLearningContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"personal_learning":false}`)
	for _, tc := range []struct{ runtime, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"personal_learning": tc.runtime})["personal-learning"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"personal_learning": tc.runtime}, true))
		it := item(t, doc, "personal-learning")
		if it.Value != "off" || it.Default != "on" || it.Locked || it.Pending != (tc.runtime == "true") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"personal-learning", "off", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
