package main

import (
	"testing"

	"git.biggo.com/0c/ime/core/decode"
)

func TestErrorLayersKeepReachabilitySeparateFromRank(t *testing.T) {
	graph := decode.TargetAudit{SurfaceSegmentable: true, ReadingReachable: true, Best: &decode.Hypothesis{}}
	for _, tc := range []struct {
		correct         bool
		rank, recovered int
		target          decode.TargetAudit
		want            string
	}{
		{true, 1, 0, graph, "production_correct"},
		{false, 16, 0, graph, "ranking_within_32"},
		{false, 0, 12, graph, "beam_or_prefix_context_pruned"},
		{false, 0, 0, graph, "outside_unpruned_top32"},
		{false, 0, 0, decode.TargetAudit{}, "surface_unreachable"},
		{false, 0, 0, decode.TargetAudit{SurfaceSegmentable: true}, "reading_or_lock_unreachable"},
		{false, 0, 0, decode.TargetAudit{ReadingReachable: true}, "prefix_pool_or_zero_score"},
	} {
		if got := layer(tc.correct, tc.rank, tc.target, tc.recovered); got != tc.want {
			t.Fatalf("%+v: %s", tc, got)
		}
	}
}

func TestRecallUsesCandidateSlotsAndLimitedFormFolding(t *testing.T) {
	rows := []decode.Hypothesis{{Text: "臺北"}, {Text: "台北"}, {Text: "再見"}}
	if rank(rows, "台北", false) != 2 || rank(rows, "台北", true) != 1 || rank(rows, "在建", true) != 0 || rank(rows, "再見", true) != 3 {
		t.Fatal("folding changed a word or renumbered slots")
	}
}
