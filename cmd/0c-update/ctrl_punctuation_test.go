package main

import (
	"testing"
)

// #168：「Ctrl＋標點打全形」經 settings --json 給設定程式；引擎載入的值不同時標示待套用，舊引擎沒回報時不猜。
func TestCtrlPunctuationContractReportsPendingAndUnknownRuntime(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"ctrl_punctuation":false}`)
	for _, tc := range []struct{ runtime, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"ctrl_punctuation": tc.runtime})["ctrl-punctuation"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"ctrl_punctuation": tc.runtime}, true))
		it := item(t, doc, "ctrl-punctuation")
		if it.Value != "off" || it.Pending != (tc.runtime == "true") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	if o, err := parseSetArgs([]string{"ctrl-punctuation", "off", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
