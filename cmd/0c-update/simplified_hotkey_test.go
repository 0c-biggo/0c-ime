package main

import "testing"

// #192：「用快捷鍵切換簡體字」經 settings --json 給設定程式；引擎載入的值不同時標示待套用（改了要重啟引擎，
// 外殼才從 health／open 拿到新值），舊引擎沒回報時不猜。
func TestSimplifiedHotkeyContractReportsPendingAndUnknownRuntime(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"simplified_hotkey":true}`)
	for _, tc := range []struct{ runtime, want string }{{"false", "off"}, {"true", "on"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"simplified_hotkey": tc.runtime})["simplified-hotkey"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"simplified_hotkey": tc.runtime}, true))
		it := item(t, doc, "simplified-hotkey")
		if it.Value != "on" || it.Pending != (tc.runtime == "false") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	o, err := parseSetArgs([]string{"simplified-hotkey", "on", "-apply", "-json"})
	if err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
	// 只改快捷鍵不是即時切換：要重啟引擎套用，外殼才知道要攔這個鍵。
	if _, live := liveSimplified([]changeView{{Key: "simplified-hotkey", To: "on"}}); live {
		t.Fatal("快捷鍵開關不能走即時切換")
	}
}
