package english

import "testing"

func TestPrefixAndCase(t *testing.T) {
	d := New([]Word{{"meeting", 10}, {"meet", 1}, {"met", 9}, {"MEETING", 2}})
	if !d.Has("Meeting") || d.Has("meetng") {
		t.Fatal("spelling lookup")
	}
	got := d.Prefix("Me", 2)
	if len(got) != 2 || got[0].Text != "Meeting" || got[1].Text != "Met" {
		t.Fatalf("%+v", got)
	}
	if d.Prefix("me_", 3) != nil {
		t.Fatal("identifiers must remain literal")
	}
}

func TestTypoChannelsAndValidWordProtection(t *testing.T) {
	d := Prototype()
	for _, tc := range []struct{ typed, want string }{{"recieve", "receive"}, {"hellp", "hello"}, {"helo", "hello"}, {"helllo", "hello"}, {"teh", "the"}, {"RECIEVE", "RECEIVE"}} {
		cs := d.Corrections(tc.typed, 1)
		if len(cs) != 1 || cs[0].Text != tc.want {
			t.Errorf("%s: %+v", tc.typed, cs)
		}
	}
	for _, text := range []string{"from", "the", "api_v2", "https://example.com"} {
		if len(d.Corrections(text, 3)) != 0 {
			t.Errorf("must not correct %s", text)
		}
	}
}
