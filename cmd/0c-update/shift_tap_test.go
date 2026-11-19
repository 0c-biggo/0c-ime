package main

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #319：「單按 Shift 的時限」經 settings --json 給設定程式；引擎回報的 shift_tap_ms 跟設定檔不同時標示待套用
// （舊引擎固定回報 300，也就是它實際用的值），沒回報時不猜。不屬於輸入習慣風格，新安裝也不鎖。
func TestShiftTapContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"shift_tap_ms":500}`)
	for _, tc := range []struct{ runtime, want string }{{"300", "300"}, {"500", "500"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"shift_tap_ms": tc.runtime})["shift-tap-ms"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"shift_tap_ms": tc.runtime}, true))
		it := item(t, doc, "shift-tap-ms")
		if it.Value != "500" || it.Default != "300" || it.Locked || it.Pending != (tc.runtime == "300") || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	fresh := t.TempDir()
	if it := item(t, buildSettingsDoc(fresh, noEngine), "shift-tap-ms"); it.Value != "300" || it.Locked {
		t.Fatalf("新安裝：%+v", it)
	}
}

// 0c-update set 只接受選單裡的毫秒數；不合法時整批不寫。
func TestRunSetShiftTap(t *testing.T) {
	base := t.TempDir()
	path := writeSettings(t, base, `{"selection":"neural"}`)
	before := readFile(t, path)
	for _, bad := range []string{"350", "0", "0.4", "400ms", "1000"} {
		_, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"shift-tap-ms", bad}}}, nop)
		if c := classify(err); c.Code != settings.CodeInvalidValue || c.Key != "shift-tap-ms" {
			t.Fatalf("%q：%+v %v", bad, c, err)
		}
	}
	if after := readFile(t, path); after != before {
		t.Fatalf("失敗時不得改動檔案：%s", after)
	}
	if _, err := runSet(base, noEngine, setOptions{pairs: [][2]string{{"shift-tap-ms", "400"}}}, nop); err != nil {
		t.Fatal(err)
	}
	if text := readFile(t, path); !strings.Contains(text, `"shift_tap_ms": 400`) {
		t.Fatalf("存檔：%s", text)
	}
	if o, err := parseSetArgs([]string{"shift-tap-ms", "600", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
