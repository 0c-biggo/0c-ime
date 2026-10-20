package lm

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"git.biggo.com/0c/ime/core/binfmt"
)

// Compact 是緊湊的唯讀語言模型。詞依字串排序，unigram 以二分搜尋查詢。
//
// bigram 依（前詞, 後詞）排序，前詞由 leftStart 隱含：每筆只存後詞 ID（3 位元組）與
// 量化後的機率（1 位元組，log10 刻度），共 4 位元組，仍可在同一前詞的一段內二分搜尋。
type Compact struct {
	bucket    map[rune][2]uint32 // 首字 → 以該字開頭的詞在排序陣列中的範圍（載入時重建）
	strs      string
	off       []uint32 // 第 i 個詞 = strs[off[i]:off[i+1]]；len = 詞數+1
	uni       []float32
	leftStart []uint32 // 以第 i 個詞為前詞的 bigram 位於 [leftStart[i], leftStart[i+1])；len = 詞數+1
	right     []byte   // 每筆 3 位元組的後詞 ID（小端序）
	q         []byte   // 每筆 1 位元組的量化 log10 機率

	quantileOnce sync.Once // UniQuantileByLen 第一次用到時才算
	quantile     []float64
}

// 量化：log10 P ∈ [−qRange, 0] 對應到 0..255；一階約 0.031（相對誤差至多約 ±3.6%）。
const qRange = 8.0

func quantize(p float64) byte {
	x := -math.Log10(p) / qRange * 255
	return byte(math.Min(255, math.Max(0, math.Round(x))))
}

func dequantize(b byte) float64 { return math.Pow(10, -float64(b)/255*qRange) }

// qTable 預先算好 256 個量化值的機率。
var qTable = func() (t [256]float64) {
	for i := range t {
		t[i] = dequantize(byte(i))
	}
	return
}()

// BuildCompact 由 unigram 與 bigram 建出 Compact。bigram 的兩個詞都必須有 unigram。
func BuildCompact(uni map[string]float64, bi map[[2]string]float64) (*Compact, error) {
	words := make([]string, 0, len(uni))
	for w, p := range uni {
		if !(p > 0 && p <= 1) {
			return nil, fmt.Errorf("%q 的機率 %g 不在 (0,1]", w, p)
		}
		words = append(words, w)
	}
	sort.Strings(words)
	if len(words) >= 1<<24 {
		return nil, errors.New("詞數超過 3 位元組 ID 的上限")
	}
	c := &Compact{}
	var sb strings.Builder
	for _, w := range words {
		c.off = append(c.off, uint32(sb.Len()))
		sb.WriteString(w)
		c.uni = append(c.uni, float32(uni[w]))
	}
	c.off = append(c.off, uint32(sb.Len()))
	c.strs = sb.String()
	c.index()
	type pair struct {
		l, r uint32
		q    byte
	}
	ps := make([]pair, 0, len(bi))
	for k, p := range bi {
		l, okl := c.find(k[0])
		w, okw := c.find(k[1])
		if !okl || !okw {
			return nil, fmt.Errorf("bigram %q→%q 的詞沒有 unigram", k[0], k[1])
		}
		if !(p > 0 && p <= 1) {
			return nil, fmt.Errorf("bigram %q→%q 的機率 %g 不在 (0,1]", k[0], k[1], p)
		}
		ps = append(ps, pair{uint32(l), uint32(w), quantize(p)})
	}
	sort.Slice(ps, func(a, b int) bool {
		if ps[a].l != ps[b].l {
			return ps[a].l < ps[b].l
		}
		return ps[a].r < ps[b].r
	})
	c.leftStart = make([]uint32, len(words)+1)
	c.right = make([]byte, 3*len(ps))
	c.q = make([]byte, len(ps))
	k := 0
	for i := range words {
		c.leftStart[i] = uint32(k)
		for k < len(ps) && int(ps[k].l) == i {
			c.right[3*k], c.right[3*k+1], c.right[3*k+2] = byte(ps[k].r), byte(ps[k].r>>8), byte(ps[k].r>>16)
			c.q[k] = ps[k].q
			k++
		}
	}
	c.leftStart[len(words)] = uint32(k)
	return c, nil
}

func (c *Compact) rightAt(i int) uint32 {
	return uint32(c.right[3*i]) | uint32(c.right[3*i+1])<<8 | uint32(c.right[3*i+2])<<16
}

// index 建立首字分桶：以同一個字開頭的詞在排序後是連續的一段。
func (c *Compact) index() {
	c.bucket = map[rune][2]uint32{}
	for i := range c.uni {
		r, _ := utf8.DecodeRuneInString(c.word(i))
		b, ok := c.bucket[r]
		if !ok {
			b[0] = uint32(i)
		}
		b[1] = uint32(i + 1)
		c.bucket[r] = b
	}
}

func (c *Compact) word(i int) string { return c.strs[c.off[i]:c.off[i+1]] }

func (c *Compact) find(w string) (int, bool) {
	r, _ := utf8.DecodeRuneInString(w)
	b, ok := c.bucket[r]
	if !ok {
		return 0, false
	}
	lo, hi := int(b[0]), int(b[1])
	i := lo + sort.Search(hi-lo, func(k int) bool { return c.word(lo+k) >= w })
	return i, i < hi && c.word(i) == w
}

func (c *Compact) Uni(w string) float64 {
	if i, ok := c.find(w); ok {
		return float64(c.uni[i])
	}
	return Unseen
}

func (c *Compact) Bi(left, w string) float64 {
	if len(c.q) == 0 {
		return 0
	}
	l, okl := c.find(left)
	if !okl || c.leftStart[l] == c.leftStart[l+1] {
		return 0
	}
	r, okr := c.find(w)
	if !okr {
		return 0
	}
	lo, hi := int(c.leftStart[l]), int(c.leftStart[l+1])
	i := lo + sort.Search(hi-lo, func(k int) bool { return c.rightAt(lo+k) >= uint32(r) })
	if i < hi && c.rightAt(i) == uint32(r) {
		return qTable[c.q[i]]
	}
	return 0
}

// HasLeft 回報是否有以 left 為前詞的 bigram。
func (c *Compact) HasLeft(left string) bool {
	if len(c.q) == 0 {
		return false
	}
	l, ok := c.find(left)
	return ok && c.leftStart[l] != c.leftStart[l+1]
}

// EachBigram 依前詞、後詞的順序列出所有 bigram 與量化後的機率，供離線檢查（例如單字排序閘門）使用。
func (c *Compact) EachBigram(fn func(left, w string, p float64)) {
	for l := 0; l+1 < len(c.leftStart); l++ {
		for i := int(c.leftStart[l]); i < int(c.leftStart[l+1]); i++ {
			fn(c.word(l), c.word(int(c.rightAt(i))), qTable[c.q[i]])
		}
	}
}

// Words 與 Bigrams 回傳規模，供報告使用。
func (c *Compact) Words() int   { return len(c.uni) }
func (c *Compact) Bigrams() int { return len(c.q) }

const (
	magic   = "ZUINLM\x00\x00"
	version = 2
)

func (c *Compact) Encode() []byte {
	w := binfmt.NewWriter(magic, version)
	w.String(c.strs)
	w.U32s(c.off)
	w.F32s(c.uni)
	w.U32s(c.leftStart)
	w.Bytes8(c.right)
	w.Bytes8(c.q)
	return w.Finish()
}

func DecodeCompact(b []byte) (*Compact, error) {
	r := binfmt.NewReader(b, magic, version)
	c := &Compact{strs: r.String(), off: r.U32s(), uni: r.F32s(), leftStart: r.U32s(), right: r.Bytes8(), q: r.Bytes8()}
	if err := r.Done(); err != nil {
		return nil, err
	}
	n := len(c.uni)
	if len(c.off) != n+1 || len(c.leftStart) != n+1 || len(c.right) != 3*len(c.q) || int(c.leftStart[n]) != len(c.q) {
		return nil, errors.New("資料檔各欄長度不一致")
	}
	for i := 0; i < n; i++ {
		if c.off[i] > c.off[i+1] || int(c.off[i+1]) > len(c.strs) {
			return nil, fmt.Errorf("詞 %d 的位移超出範圍", i)
		}
		if i > 0 && c.word(i-1) >= c.word(i) {
			return nil, errors.New("詞表未排序")
		}
		if c.leftStart[i] > c.leftStart[i+1] {
			return nil, errors.New("bigram 索引未排序")
		}
		for k := int(c.leftStart[i]); k < int(c.leftStart[i+1]); k++ {
			if int(c.rightAt(k)) >= n || (k > int(c.leftStart[i]) && c.rightAt(k-1) >= c.rightAt(k)) {
				return nil, errors.New("bigram 後詞 ID 超出範圍或未排序")
			}
		}
	}
	c.index()
	return c, nil
}

// Entries 讓 Mem 可以轉成 Compact。
func (m *Mem) Entries() (map[string]float64, map[[2]string]float64) { return m.uni, m.bi }
