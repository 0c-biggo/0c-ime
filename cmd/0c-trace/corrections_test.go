package main

import (
	"encoding/json"
	"testing"

	"git.biggo.com/0c/ime/core/proto"
)

func TestExtractOnlyExplicitChangedSelection(t *testing.T) {
	e := event{Event: "request", Request: json.RawMessage(`{"type":"key","key":"2"}`), Before: proto.Snapshot{Preedit: []proto.Segment{{Text: "布直覺"}}, Candidates: &proto.CandidateList{Start: 0, Length: 1, Items: []proto.CandidateItem{{ID: 0, Text: "布"}, {ID: 1, Text: "不"}}}}, After: proto.Snapshot{Preedit: []proto.Segment{{Text: "不直覺"}}}}
	c, ok := selectedCorrection(e)
	if !ok || c.Before != "布" || c.After != "不" {
		t.Fatal(c, ok)
	}
	for _, r := range []string{`{"type":"key","key":"Escape"}`, `{"type":"key","key":"2","private":true}`, `{"type":"commit"}`} {
		x := e
		x.Request = json.RawMessage(r)
		if _, ok := selectedCorrection(x); ok {
			t.Fatal("不能推論成手動更正", r)
		}
	}
	e.After = e.Before
	e.After.Candidates = nil
	if _, ok := selectedCorrection(e); ok {
		t.Fatal("未改字不是更正")
	}
}
