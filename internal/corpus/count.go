package corpus

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"math"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Vocab 是斷詞與計數用的詞表：詞面 → ID 與 unigram 對數機率。
type Vocab struct {
	Words []string
	id    map[string]uint32
	logp  []float64
	BOS   uint32 // 片段開頭
	EOS   uint32 // 片段結尾
}

// NewVocab 由詞與其 unigram 機率建立詞表。
func NewVocab(prob map[string]float64) *Vocab {
	v := &Vocab{id: map[string]uint32{}}
	for w := range prob {
		v.Words = append(v.Words, w)
	}
	sort.Strings(v.Words)
	v.logp = make([]float64, len(v.Words))
	for i, w := range v.Words {
		v.id[w] = uint32(i)
		v.logp[i] = math.Log(prob[w])
	}
	v.BOS, v.EOS = uint32(len(v.Words)), uint32(len(v.Words)+1)
	return v
}

// Name 回傳 ID 的詞面；片段開頭與結尾為 "<s>"、"</s>"。
func (v *Vocab) Name(id uint32) string {
	switch id {
	case v.BOS:
		return "<s>"
	case v.EOS:
		return "</s>"
	}
	return v.Words[id]
}

// Segment 以最大 unigram 機率斷詞，回傳詞 ID。片段先在「詞表沒有的單字」處切開，
// 每一段各自斷詞並呼叫 fn（詞表收了所有常用單字，所以每一段一定斷得出來）。
func (v *Vocab) Segment(chunk string, fn func(ids []uint32)) {
	start := 0
	for i, r := range chunk {
		if _, ok := v.id[chunk[i:i+utf8.RuneLen(r)]]; !ok {
			v.segment(chunk[start:i], fn)
			start = i + utf8.RuneLen(r)
		}
	}
	v.segment(chunk[start:], fn)
}

func (v *Vocab) segment(chunk string, fn func(ids []uint32)) {
	if chunk == "" {
		return
	}
	bounds := make([]int, 0, len(chunk)/3+1)
	for i := range chunk {
		bounds = append(bounds, i)
	}
	bounds = append(bounds, len(chunk))
	n := len(bounds) - 1
	best := make([]float64, n+1)
	back := make([]int, n+1)
	wid := make([]uint32, n+1)
	for i := 1; i <= n; i++ {
		best[i] = math.Inf(-1)
		for j := max(0, i-8); j < i; j++ {
			if math.IsInf(best[j], -1) {
				continue
			}
			if id, ok := v.id[chunk[bounds[j]:bounds[i]]]; ok {
				if sc := best[j] + v.logp[id]; sc > best[i] {
					best[i], back[i], wid[i] = sc, j, id
				}
			}
		}
	}
	ids := make([]uint32, 0, n)
	for i := n; i > 0; i = back[i] {
		ids = append(ids, wid[i])
	}
	for a, b := 0, len(ids)-1; a < b; a, b = a+1, b-1 {
		ids[a], ids[b] = ids[b], ids[a]
	}
	fn(ids)
}

// HanChunks 把文字在非漢字處切開，回傳長度 ≥ min 的純漢字片段。
func HanChunks(s string, min int, fn func(string)) {
	start := -1
	count := 0
	for i, r := range s {
		if zhuyin.IsHan(r) {
			if start < 0 {
				start, count = i, 0
			}
			count++
			continue
		}
		if start >= 0 && count >= min {
			fn(s[start:i])
		}
		start = -1
	}
	if start >= 0 && count >= min {
		fn(s[start:])
	}
	_ = utf8.RuneError
}

// Counts 是 n-gram 次數。
type Counts struct {
	Uni   map[uint32]uint64
	Bi    map[uint64]uint32
	Left  map[uint32]uint64 // 作為前詞的總次數
	Chunk uint64
}

func NewCounts() *Counts {
	return &Counts{Uni: map[uint32]uint64{}, Bi: map[uint64]uint32{}, Left: map[uint32]uint64{}}
}

// Add 計入一段詞 ID 序列（前後加上片段開頭與結尾）。
func (c *Counts) Add(v *Vocab, ids []uint32) {
	c.Chunk++
	prev := v.BOS
	for _, id := range append(ids, v.EOS) {
		if id != v.EOS {
			c.Uni[id]++
		}
		c.Bi[uint64(prev)<<32|uint64(id)]++
		c.Left[prev]++
		prev = id
	}
}

// JSONLDocs 逐篇讀 jsonl.gz（每行一個 JSON 物件），把 title 與 text 接起來交給 fn。
func JSONLDocs(path string, fn func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	br := bufio.NewReaderSize(gz, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var doc struct{ Title, Text string }
			if json.Unmarshal(line, &doc) == nil {
				fn(doc.Title + "\n" + doc.Text)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
