//go:build linux

package main

import "testing"

func TestStyleShiftSpaceMapping(t *testing.T) {
	asked := 0
	yes := func() bool { asked++; return true }
	no := func() bool { asked++; return false }
	if styleKey(toKey(keySpace, maskShift), true, yes) != "ShiftSpace" {
		t.Fatal("Shift Space lost modifier")
	}
	if styleKey(toKey(keySpace, maskShift), true, no) != " " {
		t.Fatal("old engine must receive space")
	}
	if styleKey(toKey(keySpace, maskControl|maskShift), true, yes) != "" {
		t.Fatal("Ctrl Shift Space must pass to host")
	}
	if styleKey(" ", false, yes) != " " || styleKey("BackTab", true, yes) != "BackTab" {
		t.Fatal("unrelated key changed")
	}
	if asked != 2 {
		t.Fatalf("capability queried %d times", asked)
	}
}

func TestHorizontalStyleLookupTable(t *testing.T) {
	table := newLookupTable([]string{"甲", "乙"}, []string{"1", "2"}, 1, true).Value().(ibusLookupTable)
	if table.Orientation != 0 || table.Cursor != 1 {
		t.Fatalf("horizontal table = %#v", table)
	}
	if newLookupTable([]string{"甲"}, []string{"1"}, 0).Value().(ibusLookupTable).Orientation != orientationVertical {
		t.Fatal("legacy orientation changed")
	}
}

// The socket object survives EOF until a caller notices it. The first style
// chord after a handoff must reopen before consulting the old open reply.
func TestFirstStyleCapabilityAfterHandoff(t *testing.T) {
	for _, first := range []string{"space", "caps", "shift"} {
		t.Run(first, func(t *testing.T) {
			e, _, sock := newTestEngine(t)
			info := func(on bool) map[string]string {
				if on {
					return map[string]string{"input_style_shift_space": "fullwidth", "input_style_caps_letter": "lower", "left_shift": "true", "right_shift": "true"}
				}
				return map[string]string{"input_style_shift_space": "disabled", "input_style_caps_letter": "platform", "left_shift": "false", "right_shift": "false"}
			}
			stop := serveEngine(t, sock, toyEngine(t), info(false))
			if e.session.styleShiftSpace() || e.session.styleCapsLower() {
				t.Fatal("old profile enabled style modifiers")
			}
			for _, on := range []bool{true, false} {
				stop()
				e.session.client.conn.Close() // keep cached session and connection pointer, like a retired peer
				stop = serveEngine(t, sock, toyEngine(t), info(on))
				var got bool
				switch first {
				case "space":
					got = e.session.styleShiftSpace()
				case "caps":
					got = e.session.styleCapsLower()
				case "shift":
					got = e.session.shiftKeys().left
				}
				if got != on {
					t.Fatalf("first %s capability = %v, want %v", first, got, on)
				}
			}
			stop()
		})
	}
}

func TestStyleCapsLetterCase(t *testing.T) {
	for _, tc := range []struct {
		key, state     uint32
		english, lower bool
		want           uint32
	}{
		{'A', maskLock, true, true, 'a'},
		{'a', maskLock | maskShift, true, true, 'A'},
		{'a', maskLock | maskShift, true, false, 'a'},
		{'A', maskLock, true, false, 'A'},
		{'A', maskShift, true, true, 'A'},
		{'a', 0, true, true, 'a'},
		{'A', maskLock, false, false, 'a'},
		{'a', maskLock | maskShift, false, false, 'A'},
	} {
		if got := foldStyleCaps(tc.key, tc.state, tc.english, tc.lower); got != tc.want {
			t.Errorf("case %+v got %q", tc, rune(got))
		}
	}
}
