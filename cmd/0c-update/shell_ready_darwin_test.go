//go:build darwin

package main

import "testing"

func TestParseAquaSession(t *testing.T) {
	if id, err := parseAquaSession("gui/502 = {\n\tsecurity context = {\n\t\tasid = 103477\n\t}\n}\n"); err != nil || id != 103477 {
		t.Fatalf("%d %v", id, err)
	}
	for _, out := range []string{"", "asid = 0\n", "asid = 18446744073709551616\n", "prefix asid = 12\n"} {
		if id, err := parseAquaSession(out); err == nil || id != 0 {
			t.Fatalf("accepted %q: %d %v", out, id, err)
		}
	}
}
