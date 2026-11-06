package session

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/rerank"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 打標點後接著打下一段時，前面已經排過、輸入沒變的段落要走段級快取（rerankMemo），不重跑 N-best。
// 摘要曾收整串的 shownSyls 長度，每打一個音節前段就失效，打字時一次也不命中（PR #51 審查）。
func TestRerankMemoHitsEarlierSegmentsWhileTyping(t *testing.T) {
	s := newShemeSessionWithKeeps(t)
	calls := 0
	s.Rerank = rerank.Options{Weight: 2, Scorer: rankFunc(func(r rerank.Request) ([]float64, error) {
		calls++
		return make([]float64, len(r.Candidates)), nil
	})}
	syls := zhuyin.ParseReading(longRerankReading)
	for i, y := range syls {
		if i > 0 && i%8 == 0 {
			key(s, "<")
		}
		typeSyl(s, string(y))
	}
	if calls == 0 {
		t.Fatal("測試前提：應排序過")
	}
	// 32 音節分成 4 段：打第 k 段時前 k−1 段每打一個音節都應命中。第 2 段起每段 8 個音節，
	// 至少 (1+2+3)×7 次；標點鍵本身也會再命中一輪。
	if s.rerankMemoHits < 42 {
		t.Fatalf("打字時段級快取只命中 %d 次", s.rerankMemoHits)
	}
	if testing.Verbose() {
		t.Logf("命中 %d 次", s.rerankMemoHits)
	}
}
