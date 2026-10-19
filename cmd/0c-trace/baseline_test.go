package main

import (
	"encoding/json"
	"git.biggo.com/0c/ime/core/proto"
	"strings"
	"testing"
	"time"
)

func traceEventForTest(seq int, before, after proto.Snapshot, r proto.Request) event {
	b, _ := json.Marshal(r)
	return event{Event: "request", Run: "test", Seq: uint64(seq), UnixUS: int64(seq), Session: 1, Server: map[string]string{"executable_sha256": "binary", "data_sha256": "data"}, Request: b, Before: before, After: after}
}
func traceSnap(text string) proto.Snapshot {
	s := proto.Snapshot{Mode: proto.ModeZhuyin, Handled: true, Cursor: proto.CursorPos{Rune: len([]rune(text))}}
	if text != "" {
		s.Preedit = []proto.Segment{{Text: text, Style: proto.StyleNormal}}
	}
	return s
}
func TestBaselineCompleteAndExplicitSelection(t *testing.T) {
	empty, a, b := traceSnap(""), traceSnap("甲"), traceSnap("乙")
	menu := a
	menu.Candidates = &proto.CandidateList{Start: 0, Length: 1, Items: []proto.CandidateItem{{ID: 0, Text: "乙"}}}
	menu2 := b
	menu2.Candidates = &proto.CandidateList{Start: 0, Length: 1, Items: []proto.CandidateItem{{ID: 0, Text: "甲"}}}
	committed := empty
	committed.Commit = "甲"
	es := []event{
		traceEventForTest(1, empty, a, proto.Request{Type: proto.Key, Key: "x"}),
		traceEventForTest(2, a, menu, proto.Request{Type: proto.Key, Key: proto.KeyDown}),
		traceEventForTest(3, menu, b, proto.Request{Type: proto.Key, Key: proto.KeyEnter}),
		traceEventForTest(4, b, menu2, proto.Request{Type: proto.Key, Key: proto.KeyDown}),
		traceEventForTest(5, menu2, a, proto.Request{Type: proto.Select, Index: 0}),
		traceEventForTest(6, a, committed, proto.Request{Type: proto.Key, Key: proto.KeyEnter}),
	}
	report := baseline(es, time.UnixMicro(0), time.UnixMicro(10), 0)
	c := report.Versions[0].Counts
	if c.Compositions != 1 || c.ChangedCompositions != 1 || c.Replacements != 2 || c.Reselections != 1 || c.CorrectionKeys != 3 || c.PointerSelections != 1 {
		t.Fatalf("%+v", c)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "甲") || strings.Contains(string(encoded), "乙") {
		t.Fatal("baseline leaked text")
	}
	// A reconnect, version change, gap, or discontinuity must not count a partial composition.
	for _, kind := range []string{"reconnect", "version", "gap", "sequence", "snapshot"} {
		copyEvents := append([]event(nil), es...)
		switch kind {
		case "reconnect":
			copyEvents[3].Reconnected = true
		case "version":
			copyEvents[3].Server = map[string]string{"executable_sha256": "other", "data_sha256": "data"}
		case "gap":
			copyEvents[3].Dropped = 1
		case "sequence":
			for i := 3; i < len(copyEvents); i++ {
				copyEvents[i].Seq++
			}
		case "snapshot":
			copyEvents[3].Before = traceSnap("丙")
		}
		r := baseline(copyEvents, time.UnixMicro(0), time.UnixMicro(10), 0)
		for _, v := range r.Versions {
			if v.Counts.Compositions != 0 {
				t.Fatalf("%s counted partial", kind)
			}
		}
	}
}
func TestBaselineOrdinaryCommitAndOpenTail(t *testing.T) {
	e, a := traceSnap(""), traceSnap("甲")
	c := e
	c.Commit = "甲"
	es := []event{traceEventForTest(1, e, a, proto.Request{Type: proto.Key, Key: "x"}), traceEventForTest(2, a, c, proto.Request{Type: proto.Commit}), traceEventForTest(3, e, a, proto.Request{Type: proto.Key, Key: "x"})}
	r := baseline(es, time.UnixMicro(0), time.UnixMicro(9), 0)
	v := r.Versions[0]
	if v.Counts.Compositions != 1 || v.Counts.Replacements != 0 || v.Excluded != 1 {
		t.Fatalf("%+v", v)
	}
	r = baseline(es, time.UnixMicro(2), time.UnixMicro(3), 0)
	if r.Versions[0].Counts.Compositions != 0 {
		t.Fatal("left censored commit counted")
	}
}

func TestBaselineTabPreviewAcceptance(t *testing.T) {
	e, a, b := traceSnap(""), traceSnap("小龍"), traceSnap("小籠包")
	preview := a
	preview.Suggestion = "小龍 → 小籠包"
	preview.SuggestionEdit = &proto.SuggestionEdit{Start: 0, Length: 2, Text: "小籠包"}
	c := e
	c.Commit = "小籠包"
	es := []event{traceEventForTest(1, e, a, proto.Request{Type: proto.Key, Key: "x"}), traceEventForTest(2, a, preview, proto.Request{Type: proto.Key, Key: proto.KeyTab}), traceEventForTest(3, preview, b, proto.Request{Type: proto.Key, Key: proto.KeyTab}), traceEventForTest(4, b, c, proto.Request{Type: proto.Commit})}
	v := baseline(es, time.UnixMicro(0), time.UnixMicro(9), 0).Versions[0]
	if v.Counts.TabPreviews != 1 || v.Counts.TabAccepts != 1 || v.Counts.Replacements != 0 {
		t.Fatalf("%+v", v.Counts)
	}
}
