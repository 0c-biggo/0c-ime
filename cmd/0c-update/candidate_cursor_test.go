package main

import "testing"

func TestCandidateCursorContractReportsPending(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"candidate_cursor":"before"}`)
	for _, runtime := range []string{"before", "after", ""} {
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"candidate_cursor": runtime}, true))
		it := item(t, doc, "candidate-cursor")
		if it.Value != "before" || it.Pending != (runtime == "after") || (it.Running != nil) != (runtime != "") || doc.PendingRestart != it.Pending {
			t.Fatalf("%+v", it)
		}
	}
	o, err := parseSetArgs([]string{"candidate-cursor", "after", "-apply", "-json"})
	if err != nil || len(o.pairs) != 1 || !o.apply || !o.json {
		t.Fatalf("%+v %v", o, err)
	}
}
