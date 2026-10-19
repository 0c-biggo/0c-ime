package session

import (
	"strings"
	"testing"

	"git.biggo.com/0c/ime/core/layout"
	"git.biggo.com/0c/ime/core/proto"
	"git.biggo.com/0c/ime/core/rerank"
	"git.biggo.com/0c/ime/core/zhuyin"
)

// longRerankReading 是 32 音節的長句：多處「什麼」與「為什麼」讓使用者選過的詞、更正與 neuralKeeps 都要檢查（#101）。
const longRerankReading = "ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ ㄕˊ ㄏㄡˋ ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ ㄕˊ ㄏㄡˋ ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ " +
	"ㄕˊ ㄏㄡˋ ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ ㄕˊ ㄏㄡˋ ㄨㄟˋ ㄕㄜˊ ㄇㄜ˙ ㄕˋ ㄕˊ ㄏㄡˋ ㄕㄜˊ ㄇㄜ˙"

// longRerankSession 是選過「什麼」（留下個人讀音與語境更正）的使用者；評分器全給 0 分，顯示不變，
// 量到的只有排序流程本身的成本。calls 記錄評分器被呼叫的次數。
func longRerankSession(b *testing.B, calls *int) *Session {
	b.Helper()
	s := newShemeSessionWithKeeps(b)
	s.Rerank = rerank.Options{Weight: 2, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		*calls++
		return make([]float64, len(r.Candidates)), nil
	})}
	shown(s, "ㄕㄜˊ ㄇㄜ˙")
	selectText(b, s, 0, "什麼")
	if got := commitText(s); got != "什麼" {
		b.Fatalf("commit %q", got)
	}
	if len(s.model.Store.Readings) == 0 {
		b.Fatal("測試前提：應留下個人讀音")
	}
	return s
}

// BenchmarkRerankCachedLongSegment 是 32 音節段落沒有變動時重排一次的成本（快取命中）：例如打標點後
// 接著打下一段時，前面的段落每鍵都會重新走一次 rerankSegment。
func BenchmarkRerankCachedLongSegment(b *testing.B) {
	calls := 0
	s := longRerankSession(b, &calls)
	typeReadingLocal(s, longRerankReading)
	if got := len([]rune(s.snapshot().Text())); got != 32 || calls == 0 {
		b.Fatalf("測試前提：應顯示 32 字並排序過，實得 %d 字、%d 次", got, calls)
	}
	s.allowRerank = true // 平常只在處理按鍵時開啟
	before := calls
	b.ReportAllocs()
	for b.Loop() {
		s.rerankPath()
	}
	if calls != before {
		b.Fatalf("沒有變動的段落應走快取：評分器多被呼叫 %d 次", calls-before)
	}
}

// BenchmarkOverturnsUserLongSegment 是 32 音節段落算一次排除候選的成本；快取命中時不再付這一筆（#101）。
func BenchmarkOverturnsUserLongSegment(b *testing.B) {
	calls := 0
	s := longRerankSession(b, &calls)
	typeReadingLocal(s, longRerankReading)
	s.configureDocumentContext(0)
	s.dec.Evidence = nil
	s.configureCorrections(0, len(s.syls), true)
	rows, _ := s.dec.NBest(s.lattice(), nil, s.Opt.PrefixAware, 8)
	if len(rows) < 2 {
		b.Fatalf("測試前提：應有多個候選，實得 %d", len(rows))
	}
	b.ReportAllocs()
	for b.Loop() {
		s.vetoRepeats(rows, s.overturnsUser(rows, 0, len(s.syls)))
	}
}

// BenchmarkRerankTypingLongSentence 逐鍵打出 32 音節、每 8 音節一個逗號的長句，回報每鍵平均耗時（ns/key）。
// 打後面的段落時，前面已經排過的段落走快取。
func BenchmarkRerankTypingLongSentence(b *testing.B) {
	calls := 0
	s := longRerankSession(b, &calls)
	syls := zhuyin.ParseReading(longRerankReading)
	keys := 0
	b.ReportAllocs()
	for b.Loop() {
		for i, y := range syls {
			if i > 0 && i%8 == 0 {
				key(s, "<")
				keys++
			}
			keys += len(layout.Standard.Keys(y))
			typeSyl(s, string(y))
		}
		if got := s.snapshot().Text(); strings.Count(got, "，") != 3 {
			b.Fatalf("長句 %q", got)
		}
		s.Handle(proto.Request{Type: proto.Cancel})
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(keys), "ns/key")
}
