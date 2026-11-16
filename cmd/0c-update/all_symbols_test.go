package main

import "testing"

// #282：「標點寬度」選 all 時，引擎回報 all_symbols_halfwidth 才算已套用；舊引擎只回報 shift_symbol_halfwidth 時
// 照舊標示待套用。Ctrl＋標點與聲調選填比的是實際值（Effective），不會一直顯示待套用。
func TestAllSymbolsHalfwidthContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"shift_symbol_halfwidth":true,"all_symbols_halfwidth":true,"ctrl_punctuation":false,"toneless":true}`)
	for _, tc := range []struct {
		shift, all, want string
		pending          bool
	}{{"true", "true", "all", false}, {"true", "", "on", true}, {"true", "false", "on", true}, {"false", "", "off", true}} {
		info := map[string]string{"shift_symbol_halfwidth": tc.shift, "all_symbols_halfwidth": tc.all, "ctrl_punctuation": "true", "toneless": "false"}
		if v := runningValues(info)["shift-symbol-halfwidth"]; v != tc.want {
			t.Fatalf("%+v：running %q", tc, v)
		}
		doc := buildSettingsDoc(base, modelStatusDial(info, true))
		if it := item(t, doc, "shift-symbol-halfwidth"); it.Value != "all" || it.Pending != tc.pending {
			t.Fatalf("%+v：%+v", tc, it)
		}
		for _, key := range []string{"ctrl-punctuation", "toneless"} {
			if it := item(t, doc, key); it.Pending || !it.Disabled {
				t.Fatalf("%s 應依實際值比較、顯示停用：%+v", key, it)
			}
		}
	}
	if o, err := parseSetArgs([]string{"shift-symbol-halfwidth", "all", "-apply", "-json"}); err != nil || len(o.pairs) != 1 || !o.apply {
		t.Fatalf("%+v %v", o, err)
	}
}
