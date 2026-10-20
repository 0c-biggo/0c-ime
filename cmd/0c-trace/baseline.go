package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/proto"
)

// 不保存文字的觀察基線。只有完整、連續且版本一致的組字進入操作分母。
type baselineCounts struct {
	Compositions        int `json:"complete_committed_compositions"`
	Characters          int `json:"committed_characters"`
	Keys                int `json:"key_requests"`
	ChangedCompositions int `json:"compositions_with_explicit_replacement"`
	Replacements        int `json:"explicit_replacements"`
	Reselections        int `json:"overlapping_reselections_without_intervening_edit"`
	CorrectionKeys      int `json:"navigation_and_selection_keys_ending_in_replacement"`
	PointerSelections   int `json:"pointer_or_semantic_selections"`
	Backspaces          int `json:"backspace_keys"`
	SelectedRewrites    int `json:"selected_ranges_changed_during_append"`
	TabPreviews         int `json:"tab_rewrite_previews"`
	TabAccepts          int `json:"tab_accepts"`
}
type baselineVersion struct {
	Server    string           `json:"server_sha256"`
	Data      string           `json:"data_sha256"`
	Requests  int              `json:"requests"`
	First     int64            `json:"first_unix_us"`
	Last      int64            `json:"last_unix_us"`
	Excluded  int              `json:"incomplete_or_discontinuous_compositions"`
	Counts    baselineCounts   `json:"counts"`
	Latency   map[string]int64 `json:"request_latency_us"`
	durations []int64
}
type baselineReport struct {
	Schema    int                `json:"schema"`
	Since     string             `json:"since"`
	Until     string             `json:"until_exclusive"`
	Events    int                `json:"events"`
	Malformed int                `json:"malformed_lines_in_retained_files"`
	Dropped   int                `json:"reported_dropped_events"`
	Versions  []*baselineVersion `json:"versions"`
	Limits    []string           `json:"limits"`
}
type baselineComposition struct {
	v          *baselineVersion
	counts     baselineCounts
	valid      bool
	navigation int
	selected   []correction
	last       proto.Snapshot
}

func hasComposition(s proto.Snapshot) bool { return len(s.Preedit) > 0 || s.Candidates != nil }
func sameComposition(a, b proto.Snapshot) bool {
	a.Revision = 0
	b.Revision = 0
	a.Commit = ""
	b.Commit = ""
	a.Handled = false
	b.Handled = false
	a.Notices = nil
	b.Notices = nil
	return reflect.DeepEqual(a, b)
}
func navigation(r proto.Request) bool {
	if r.Type != proto.Key {
		return false
	}
	switch r.Key {
	case proto.KeyLeft, proto.KeyRight, proto.KeyUp, proto.KeyDown, proto.KeyPageUp, proto.KeyPageDown:
		return true
	}
	return false
}
func appendInput(e event, r proto.Request) bool {
	return r.Type == proto.Key && utf8.RuneCountInString(r.Key) == 1 && e.Before.Candidates == nil && e.Before.Cursor.Rune == utf8.RuneCountInString(e.Before.Text()) && e.After.Commit == ""
}
func baseline(events []event, since, until time.Time, bad int) baselineReport {
	report := baselineReport{Schema: 1, Since: since.Format(time.RFC3339Nano), Until: until.Format(time.RFC3339Nano), Malformed: bad,
		Limits: []string{"Ordinary commits are not correctness labels; no overall accuracy or general correct-to-wrong rate is inferred.", "Counts use complete committed compositions with continuous snapshots and one binary/data version; cancellation, open tails, reconnects and gaps are excluded.", "Correction keys count the contiguous navigation/candidate-key run ending in an explicit replacement; retyping and external document edits are not attributed as correction intent.", "Overlapping reselections and selected-range rewrites are observable proxies, not all real-world corrections; edits clear positional evidence.", "Tab acceptance is not proof of correctness or net saved keys. No unaccepted suggestion is classified as wrong.", "Latency includes transport/retries; version cohorts contain different text and adaptive histories, so before/after is descriptive, not causal."}}
	versions := map[string]*baselineVersion{}
	active := map[string]*baselineComposition{}
	sequences := map[string]uint64{}
	invalidate := func(k string) {
		if c := active[k]; c != nil {
			c.v.Excluded++
			delete(active, k)
		}
	}
	for _, e := range events {
		if e.UnixUS < since.UnixMicro() || e.UnixUS >= until.UnixMicro() {
			continue
		}
		if prev := sequences[e.Run]; prev > 0 && e.Seq > prev+1 {
			for k := range active {
				if strings.HasPrefix(k, e.Run+"/") {
					invalidate(k)
				}
			}
		}
		if e.Seq > 0 {
			sequences[e.Run] = e.Seq
		}
		report.Events++
		report.Dropped += e.Dropped
		if e.Dropped > 0 {
			for k := range active {
				invalidate(k)
			}
		}
		k := fmt.Sprintf("%s/%d", e.Run, e.Session)
		if e.Event != "request" {
			switch e.Event {
			case "activate", "reset", "close", "bypass":
				invalidate(k)
			}
			continue
		}
		server, data := e.Server["executable_sha256"], e.Server["data_sha256"]
		vk := server + "/" + data
		v := versions[vk]
		if v == nil {
			v = &baselineVersion{Server: server, Data: data, First: e.UnixUS}
			versions[vk] = v
		}
		v.Requests++
		v.Last = e.UnixUS
		v.durations = append(v.durations, e.DurationUS)
		var r proto.Request
		if json.Unmarshal(e.Request, &r) != nil || e.Reconnected || r.Private || e.After.Mode == proto.ModeEnglish || (r.Type == proto.Pause && r.On) {
			invalidate(k)
			continue
		}
		c := active[k]
		if c != nil && (c.v != v || !sameComposition(c.last, e.Before)) {
			invalidate(k)
			c = nil
		}
		if c == nil {
			if !hasComposition(e.Before) && !hasComposition(e.After) && e.After.Commit == "" {
				continue
			}
			c = &baselineComposition{v: v, valid: !hasComposition(e.Before) && server != "" && data != ""}
			active[k] = c
		}
		if r.Type == proto.Key {
			c.counts.Keys++
		}
		if r.Type == proto.Key && r.Key == proto.KeyBackspace {
			c.counts.Backspaces++
		}
		if navigation(r) || (r.Type == proto.Key && e.Before.Candidates != nil) {
			c.navigation++
		} else if r.Type != proto.Select {
			c.navigation = 0
		}
		if e.After.SuggestionEdit != nil && e.Before.SuggestionEdit == nil && r.Type == proto.Key && r.Key == proto.KeyTab {
			c.counts.TabPreviews++
		}
		if (r.Type == proto.Accept || r.Type == proto.Key && r.Key == proto.KeyTab) && e.Before.Suggestion != "" && e.Before.Candidates == nil && e.After.Handled && e.After.Text() == e.Before.SuggestedText() {
			c.counts.TabAccepts++
		}
		if chosen, ok := selectedCorrection(e); ok {
			c.counts.Replacements++
			c.counts.CorrectionKeys += c.navigation
			if r.Type == proto.Select {
				c.counts.PointerSelections++
			}
			for _, old := range c.selected {
				if old.Start < chosen.Start+utf8.RuneCountInString(chosen.After) && chosen.Start < old.Start+utf8.RuneCountInString(old.After) {
					c.counts.Reselections++
					break
				}
			}
			kept := c.selected[:0]
			for _, old := range c.selected {
				if old.Start+utf8.RuneCountInString(old.After) <= chosen.Start || chosen.Start+utf8.RuneCountInString(chosen.After) <= old.Start {
					kept = append(kept, old)
				}
			}
			c.selected = append(kept, chosen)
			c.navigation = 0
		} else if appendInput(e, r) {
			after := []rune(e.After.Text())
			kept := c.selected[:0]
			for _, old := range c.selected {
				end := old.Start + utf8.RuneCountInString(old.After)
				if end <= len(after) && string(after[old.Start:end]) == old.After {
					kept = append(kept, old)
				} else {
					c.counts.SelectedRewrites++
				}
			}
			c.selected = kept
		} else if r.Type == proto.Backspace || r.Type == proto.Key && r.Key == proto.KeyBackspace || e.Before.Text() != e.After.Text() && e.After.Commit == "" {
			c.selected = nil
		}
		c.last = e.After
		if e.After.Commit != "" {
			if c.valid {
				c.counts.Compositions = 1
				c.counts.Characters = utf8.RuneCountInString(e.After.Commit)
				if c.counts.Replacements > 0 {
					c.counts.ChangedCompositions = 1
				}
				dst, src := reflect.ValueOf(&v.Counts).Elem(), reflect.ValueOf(c.counts)
				for i := 0; i < dst.NumField(); i++ {
					dst.Field(i).SetInt(dst.Field(i).Int() + src.Field(i).Int())
				}
			} else {
				v.Excluded++
			}
			delete(active, k)
		} else if !hasComposition(e.After) {
			invalidate(k)
		}
	}
	for k := range active {
		invalidate(k)
	}
	for _, v := range versions {
		sort.Slice(v.durations, func(i, j int) bool { return v.durations[i] < v.durations[j] })
		v.Latency = map[string]int64{}
		for _, p := range []int{50, 95, 99, 100} {
			v.Latency[fmt.Sprintf("p%d", p)] = v.durations[(len(v.durations)-1)*p/100]
		}
		report.Versions = append(report.Versions, v)
	}
	sort.Slice(report.Versions, func(i, j int) bool {
		a, b := report.Versions[i], report.Versions[j]
		if a.First != b.First {
			return a.First < b.First
		}
		return a.Server+a.Data < b.Server+b.Data
	})
	return report
}
