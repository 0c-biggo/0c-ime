package main

import "testing"

// #219：macOS 一律處理 caps_lock（核心記住開 Caps Lock 前的模式，關閉時回到那個模式）；Windows、Linux 照設定。
func TestCapsLockRequestsByPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos    string
		setting bool
		want    bool
	}{
		{"darwin", false, true}, {"darwin", true, true},
		{"linux", false, false}, {"linux", true, true},
		{"windows", false, false}, {"windows", true, true},
	} {
		if got := capsLockRequests(tc.goos, tc.setting); got != tc.want {
			t.Errorf("capsLockRequests(%q, %v) = %v，要 %v", tc.goos, tc.setting, got, tc.want)
		}
	}
}
