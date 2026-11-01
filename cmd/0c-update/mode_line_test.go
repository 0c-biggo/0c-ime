package main

import "testing"

// #244：狀態輸出不再寫「自動更新：manual」；顯示名稱與選單相同，方括號是 0c-update mode 接受的值。
func TestModeLine(t *testing.T) {
	for mode, want := range map[string]string{
		"auto":   "更新方式：自動（建議） [auto]",
		"ask":    "更新方式：下載後詢問 [ask]",
		"manual": "更新方式：手動檢查 [manual]",
		"future": "更新方式：future",
	} {
		if got := modeLine(mode); got != want {
			t.Errorf("modeLine(%q) = %q，應為 %q", mode, got, want)
		}
	}
}
