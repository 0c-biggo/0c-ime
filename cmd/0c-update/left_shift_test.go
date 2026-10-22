package main

import (
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
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
			if it.Value != settings.Default().ToggleKey || it.Default != settings.Default().ToggleKey || it.ValueLabels["both"] != "左右 Shift" {
				t.Fatalf("UI contract: %+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到中英切換鍵")
}

// #206：Windows 上自己設定中英切換鍵（新鍵或舊鍵）就記下來，新版引擎啟動時不把右 Shift 換成左右 Shift；改別的設定不算。
func TestChoosingToggleKeyKeepsItFromBothShiftAdoption(t *testing.T) {
	for _, tc := range []struct {
		goos  string
		pairs [][2]string
		want  bool
	}{
		{"windows", [][2]string{{"toggle-key", "right"}}, true},
		{"windows", [][2]string{{"selection", "statistical"}, {"left-shift", "off"}}, true},
		{"windows", [][2]string{{"selection-keys", "1234qweras"}}, false},
		{"linux", [][2]string{{"toggle-key", "right"}}, false},
		{"darwin", [][2]string{{"toggle-key", "right"}}, false},
	} {
		if got := choosesToggleKey(tc.goos, tc.pairs); got != tc.want {
			t.Fatalf("%s %v：%v", tc.goos, tc.pairs, got)
		}
	}
}
