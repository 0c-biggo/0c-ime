package datadir

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"git.biggo.com/0c/ime/core/personal"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/internal/englishpack"
)

var benchmarkSnapshot proto.Snapshot

func BenchmarkEnglishPackLoad(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "pack")
	if err := englishpack.Compile("../../data/english", dir); err != nil {
		b.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	d, _, err := englishpack.Load(dir)
	if err != nil {
		b.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	runtime.KeepAlive(d)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, _, err := englishpack.Load(dir)
		if err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(d)
	}
	b.StopTimer()
	b.ReportMetric(float64(retained), "retained-B")
}

// Warm session events with the full English pack and toy Chinese statistics.
// This is a shared-core microbenchmark, not native desktop latency or accuracy.
func BenchmarkFullPackMixedKeys(b *testing.B) {
	benchmarkFullPackMixedKeys(b, false, false)
}

func BenchmarkFullPackMixedKeysWithPersonalStore(b *testing.B) {
	benchmarkFullPackMixedKeys(b, true, false)
}

func BenchmarkFullPackEnglishModeKeys(b *testing.B) {
	benchmarkFullPackMixedKeys(b, false, true)
}

func BenchmarkFullPackEnglishModeKeysWithPersonalStore(b *testing.B) {
	benchmarkFullPackMixedKeys(b, true, true)
}

func benchmarkFullPackMixedKeys(b *testing.B, saturated, englishMode bool) {
	dir := toyWithEnglish(b)
	e, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	e.MixedInput = true
	e.EnglishAutocomplete = englishMode
	if saturated {
		for day := 100; day < 103; day++ {
			e.Model.AdvanceTime(int64(day) * 86400)
			for i := 0; i < personal.MaxEnglishObservations; i++ {
				e.Model.LearnEnglish([]personal.EnglishCommit{{Text: fmt.Sprintf("project_%04d", i)}})
			}
		}
		// Include nonzero decay work rather than measuring only fresh records.
		e.Model.AdvanceTime(103 * 86400)
	}
	id := e.OpenSession()
	if englishMode {
		if _, err := e.Do(id, proto.Request{Type: proto.Mode, On: true}); err != nil {
			b.Fatal(err)
		}
	}
	var events []proto.Request
	texts := []string{"ji3helloji3", "configuration", "recieve", "https://example.com/a?q=1", "I need notification", "aji3t "}
	if englishMode {
		texts = []string{"hello", "configuration", "recieve", "teh", "https://example.com/a?q=1", "I need notification"}
	}
	for _, text := range texts {
		for _, k := range text {
			events = append(events, proto.Request{Type: proto.Key, Key: string(k)})
		}
		events = append(events, proto.Request{Type: proto.Cancel})
	}
	// Warm the same journey before measuring without learning artificial words.
	for _, event := range events {
		if _, err := e.Do(id, event); err != nil {
			b.Fatal(err)
		}
	}
	samples := make([]int64, 0, min(b.N, 100000))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		benchmarkSnapshot, err = e.Do(id, events[i%len(events)])
		if err != nil {
			b.Fatal(err)
		}
		if len(samples) < cap(samples) {
			samples = append(samples, time.Since(start).Nanoseconds())
		}
	}
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) > 0 {
		b.ReportMetric(float64(samples[(len(samples)-1)*95/100]), "p95-ns")
		b.ReportMetric(float64(samples[(len(samples)-1)*99/100]), "p99-ns")
	}
}
