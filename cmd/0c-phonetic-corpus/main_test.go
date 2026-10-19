package main

import (
	"strings"
	"testing"
)

func TestSpanDomain(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{{"輸入", true}, {"輸入法", true}, {"字", false}, {"輸入a", false}, {strings.Repeat("字", 33), false}, {strings.Repeat("字", 32), true}, {"新 圖", false}} {
		if validText(tc.text) != tc.want {
			t.Fatalf("wrong domain: %q", tc.text)
		}
	}
}
