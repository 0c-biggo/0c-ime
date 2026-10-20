package main

import (
	"github.com/0c-biggo/0c-ime/internal/settings"
	"testing"
)

func TestEnglishAutocompleteSettingContractReportsPendingAndUnknownRuntime(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"english_autocomplete":true}`)
	for _, tc := range []struct{ runtime, want string }{{"true", "on"}, {"false", "off"}, {"", ""}} {
		v, ok := runningValues(map[string]string{"english_autocomplete": tc.runtime})["english-autocomplete"]
		if v != tc.want || ok != (tc.runtime != "") {
			t.Fatalf("runtime %q: %q %v", tc.runtime, v, ok)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"english_autocomplete": tc.runtime}, true))
		it := item(t, doc, "english-autocomplete")
		if it.Value != "on" || it.Pending != (tc.runtime == "false") || doc.PendingRestart != it.Pending || (it.Running != nil) != ok {
			t.Fatalf("pending contract: %+v", it)
		}
	}
	o, err := parseSetArgs([]string{"english-autocomplete", "off", "-apply", "-json"})
	if err != nil || len(o.pairs) != 1 || !o.apply || !o.json {
		t.Fatalf("%+v %v", o, err)
	}
	var found bool
	for _, it := range settings.Default().Items() {
		if it.Key == "english-autocomplete" {
			found = true
			if it.Value != "off" || it.Label == "" {
				t.Fatalf("UI contract: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("user-facing toggle missing")
	}
}
