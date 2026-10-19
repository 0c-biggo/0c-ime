package phoneticdata

import "testing"

func TestObservedToneNeverInferredFromTarget(t *testing.T) {
	for _, c := range []struct {
		raw, base, status string
		tone              int
		zero              bool
	}{
		{"ma", "ㄇㄚ", "unknown", 0, false}, {"ma5", "ㄇㄚ", "explicit", 5, false},
		{"mǎ", "ㄇㄚ", "explicit", 3, false}, {"ma\u030c", "ㄇㄚ", "explicit", 3, false},
		{"lü4", "ㄌㄩ", "explicit", 4, false}, {"lu:4", "ㄌㄩ", "explicit", 4, false},
		{"MA1", "ㄇㄚ", "explicit", 1, false}, {"ma0", "ㄇㄚ", "explicit", 5, true},
	} {
		r, err := Pinyin(c.raw, c.zero)
		if err != nil || r.Base != c.base || r.Tone != c.tone || r.ToneStatus != c.status || r.Raw != c.raw {
			t.Fatalf("%+v -> %+v %v", c, r, err)
		}
	}
	for _, raw := range []string{"", "ma0", "mǎ3", "māá", "ma6", "not-syllable", "ma ma"} {
		if _, err := Pinyin(raw, false); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
