package englishpack

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/core/english"
)

var benchmarkCandidates []english.Candidate

// Product query cost with the actual shipped pack, including common short
// prefixes, typo channels and a worst-length non-word. No private input is used.
func BenchmarkFullPackQueries(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "pack")
	if err := Compile("../../data/english", dir); err != nil {
		b.Fatal(err)
	}
	d, _, err := Load(dir)
	if err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		inputs []string
		query  func(string, int) []english.Candidate
	}{
		{"Prefix", []string{"co", "re", "confi", "notifi", "auto", "zoo", "hyper", "wo", "mic", "configuration"}, d.Prefix},
		{"Correction", []string{"recieve", "notifcation", "configuraton", "mispelled", "zoolgoy", "teh", "helllo", "zzzzzz", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}, d.Corrections},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			samples := make([]int64, 0, min(b.N, 100000))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				benchmarkCandidates = tc.query(tc.inputs[i%len(tc.inputs)], 5)
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
		})
	}
}
