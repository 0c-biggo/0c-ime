package lexicon

import (
	"testing"

	"git.biggo.com/0c/ime/core/zhuyin"
)

func TestNeutralParticles(t *testing.T) {
	m := NewMem()
	for _, e := range []struct{ w, r string }{
		{"喔", "ㄛ"}, {"啊", "ㄛ"}, {"哦", "ㄛˊ"}, {"阿", "ㄜ"}, {"餓", "ㄜˋ"}, {"呃", "ㄜˋ"},
		{"啦", "ㄌㄚ˙"}, {"拉", "ㄌㄚ"}, {"嗎", "ㄇㄚ"}, {"西", "ㄒㄧ"}, {"東西", "ㄉㄨㄥ ㄒㄧ˙"}, {"安", "ㄢ"},
	} {
		m.Add(Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
	}
	src := NeutralParticles(m)
	surfaces := func(es []Entry) (out []string) {
		for _, e := range es {
			out = append(out, e.Surface)
			if zhuyin.Key(e.Reading) != zhuyin.Key(es[0].Reading) {
				t.Fatalf("mixed readings: %+v", es)
			}
		}
		return out
	}
	for _, tc := range []struct {
		reading string
		want    []string
	}{
		{"ㄛ˙", []string{"喔", "哦"}}, {"ㄜ˙", []string{"呃"}}, {"ㄌㄚ˙", []string{"啦"}},
		{"ㄒㄧ˙", nil}, {"ㄢ˙", nil}, {"ㄛ", []string{"喔", "啊"}},
	} {
		syls := zhuyin.ParseReading(tc.reading)
		got := surfaces(src.Exact(syls))
		if len(got) != len(tc.want) {
			t.Fatalf("%s Exact = %v want %v", tc.reading, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s Exact = %v want %v", tc.reading, got, tc.want)
			}
		}
		if es := src.Exact(syls); len(es) > 0 && zhuyin.Key(es[0].Reading) != tc.reading {
			t.Fatalf("%s fallback keeps the typed neutral reading: %+v", tc.reading, es)
		}
		if src.HasPrefix(syls) != (len(tc.want) > 0) || len(src.Prefixed(syls)) != len(tc.want) {
			t.Fatalf("%s prefix lookups disagree with Exact", tc.reading)
		}
	}
}
