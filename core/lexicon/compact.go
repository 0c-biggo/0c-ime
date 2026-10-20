package lexicon

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/0c-biggo/0c-ime/core/binfmt"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Compact 是緊湊的唯讀詞庫。詞條依讀音（音節 ID 序列）排序，讀音以某前綴開頭的詞條
// 在陣列中是連續的一段，所以精確查詢與前綴查詢都是二分搜尋。沒有指標、沒有每節點的 map，
// 可以原樣寫進資料包（docs/19 的執行期資料）。
type Compact struct {
	syls   []zhuyin.Syllable // 音節 ID → 音節，依字串排序
	ids    map[zhuyin.Syllable]uint16
	rd     []uint16 // 所有讀音串接
	ent    []centry // 依讀音排序
	strs   string   // 所有詞面串接
	byText []uint32 // 詞條索引，依詞面排序，供 Has
	// longest 是最長的詞條讀音音節數。由詞條算出、不寫進資料檔，所以舊資料包也有值、格式不變（#142）。
	longest int
}

type centry struct {
	rdOff, sfOff uint32
	sfLen        uint16
	rdLen, flags uint8
	prw          float32
}

const (
	flagCorrection = 1
	flagRare       = 2
)

// Build 由詞條建出 Compact。相同（詞面, 讀音）的詞條保留 P(r|w) 較大者。
func Build(entries []Entry) (*Compact, error) {
	set := map[zhuyin.Syllable]bool{}
	for _, e := range entries {
		if len(e.Reading) == 0 || len(e.Reading) > 255 {
			return nil, fmt.Errorf("%q 的讀音長度 %d 不合法", e.Surface, len(e.Reading))
		}
		for _, s := range e.Reading {
			set[s] = true
		}
	}
	if len(set) > 1<<16 {
		return nil, errors.New("音節種類超過 65536")
	}
	c := &Compact{}
	for s := range set {
		c.syls = append(c.syls, s)
	}
	sort.Slice(c.syls, func(i, j int) bool { return c.syls[i] < c.syls[j] })
	c.index()

	type keyed struct {
		rd []uint16
		e  Entry
	}
	ks := make([]keyed, 0, len(entries))
	for _, e := range entries {
		ks = append(ks, keyed{c.encode(e.Reading), e})
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if d := cmpIDs(ks[i].rd, ks[j].rd); d != 0 {
			return d < 0
		}
		return ks[i].e.Surface < ks[j].e.Surface
	})
	var strs strings.Builder
	seen := map[string]int{} // 詞面 → 在 strs 的位移
	for _, k := range ks {
		if n := len(c.ent); n > 0 {
			last := &c.ent[n-1]
			if cmpIDs(c.rd[last.rdOff:last.rdOff+uint32(last.rdLen)], k.rd) == 0 && c.surfaceAt(last, strs.String()) == k.e.Surface {
				if float32(k.e.PRW) > last.prw {
					last.prw = float32(k.e.PRW)
					last.flags = flagsOf(k.e)
				}
				continue
			}
		}
		off, ok := seen[k.e.Surface]
		if !ok {
			off = strs.Len()
			seen[k.e.Surface] = off
			strs.WriteString(k.e.Surface)
		}
		if len(k.e.Surface) > 1<<16-1 {
			return nil, fmt.Errorf("詞面過長：%q", k.e.Surface)
		}
		c.ent = append(c.ent, centry{
			rdOff: uint32(len(c.rd)), rdLen: uint8(len(k.rd)),
			sfOff: uint32(off), sfLen: uint16(len(k.e.Surface)),
			prw: float32(k.e.PRW), flags: flagsOf(k.e),
		})
		c.rd = append(c.rd, k.rd...)
	}
	c.strs = strs.String()
	c.buildByText()
	c.measure()
	return c, nil
}

func flagsOf(e Entry) uint8 {
	var f uint8
	if e.Correction {
		f |= flagCorrection
	}
	if e.Rare {
		f |= flagRare
	}
	return f
}

func (c *Compact) measure() {
	c.longest = 0
	for i := range c.ent {
		c.longest = max(c.longest, int(c.ent[i].rdLen))
	}
}

// LongestWord 回報最長的詞條讀音音節數（decode.MaxLen 依它決定搜尋長度）。
func (c *Compact) LongestWord() int { return c.longest }

func (c *Compact) index() {
	c.ids = make(map[zhuyin.Syllable]uint16, len(c.syls))
	for i, s := range c.syls {
		c.ids[s] = uint16(i)
	}
}

func (c *Compact) buildByText() {
	c.byText = make([]uint32, len(c.ent))
	for i := range c.byText {
		c.byText[i] = uint32(i)
	}
	// 同一個詞面有多個讀音時以儲存順序（依讀音排序）決勝。只比詞面的話，同詞面之間的順序由
	// sort.Slice 的內部實作決定，換一版 Go 就不同：Lookup 的順序、lexicon.bin 的內容、
	// 以及「同分取第一個」的標音結果（0c-import 的路名讀音）都會跟著變。
	sort.Slice(c.byText, func(i, j int) bool {
		si, sj := c.surface(&c.ent[c.byText[i]]), c.surface(&c.ent[c.byText[j]])
		if si != sj {
			return si < sj
		}
		return c.byText[i] < c.byText[j]
	})
}

func (c *Compact) encode(syls []zhuyin.Syllable) []uint16 {
	out := make([]uint16, len(syls))
	for i, s := range syls {
		out[i] = c.ids[s]
	}
	return out
}

// lookupIDs 把查詢轉成音節 ID；含有詞庫裡沒有的音節時回傳 false。
func (c *Compact) lookupIDs(syls []zhuyin.Syllable) ([]uint16, bool) {
	out := make([]uint16, len(syls))
	for i, s := range syls {
		id, ok := c.ids[s]
		if !ok {
			return nil, false
		}
		out[i] = id
	}
	return out, true
}

func cmpIDs(a, b []uint16) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

func (c *Compact) reading(e *centry) []uint16 { return c.rd[e.rdOff : e.rdOff+uint32(e.rdLen)] }
func (c *Compact) surface(e *centry) string   { return c.strs[e.sfOff : e.sfOff+uint32(e.sfLen)] }
func (c *Compact) surfaceAt(e *centry, strs string) string {
	return strs[e.sfOff : e.sfOff+uint32(e.sfLen)]
}

func (c *Compact) entry(e *centry) Entry {
	ids := c.reading(e)
	r := make([]zhuyin.Syllable, len(ids))
	for i, id := range ids {
		r[i] = c.syls[id]
	}
	return Entry{Surface: c.surface(e), Reading: r, PRW: float64(e.prw), Correction: e.flags&flagCorrection != 0, Rare: e.flags&flagRare != 0}
}

// lower 回傳第一個讀音 ≥ q 的詞條位置。
func (c *Compact) lower(q []uint16) int {
	return sort.Search(len(c.ent), func(i int) bool { return cmpIDs(c.reading(&c.ent[i]), q) >= 0 })
}

func hasPrefixIDs(r, q []uint16) bool { return len(r) >= len(q) && cmpIDs(r[:len(q)], q) == 0 }

func (c *Compact) Exact(syls []zhuyin.Syllable) []Entry {
	q, ok := c.lookupIDs(syls)
	if !ok {
		return nil
	}
	var out []Entry
	for i := c.lower(q); i < len(c.ent) && cmpIDs(c.reading(&c.ent[i]), q) == 0; i++ {
		out = append(out, c.entry(&c.ent[i]))
	}
	return out
}

func (c *Compact) Prefixed(syls []zhuyin.Syllable) []Entry {
	q, ok := c.lookupIDs(syls)
	if !ok {
		return nil
	}
	var out []Entry
	for i := c.lower(q); i < len(c.ent) && hasPrefixIDs(c.reading(&c.ent[i]), q); i++ {
		out = append(out, c.entry(&c.ent[i]))
	}
	return out
}

func (c *Compact) HasPrefix(syls []zhuyin.Syllable) bool {
	q, ok := c.lookupIDs(syls)
	if !ok {
		return false
	}
	i := c.lower(q)
	return i < len(c.ent) && hasPrefixIDs(c.reading(&c.ent[i]), q)
}

func (c *Compact) Has(surface string) bool {
	i := sort.Search(len(c.byText), func(i int) bool { return c.surface(&c.ent[c.byText[i]]) >= surface })
	return i < len(c.byText) && c.surface(&c.ent[c.byText[i]]) == surface
}

// Lookup 回傳某詞面的所有讀音。
func (c *Compact) Lookup(surface string) []Entry {
	i := sort.Search(len(c.byText), func(i int) bool { return c.surface(&c.ent[c.byText[i]]) >= surface })
	var out []Entry
	for ; i < len(c.byText) && c.surface(&c.ent[c.byText[i]]) == surface; i++ {
		out = append(out, c.entry(&c.ent[c.byText[i]]))
	}
	return out
}

// Len 回傳詞條數。
func (c *Compact) Len() int { return len(c.ent) }

// All 依儲存順序走訪所有詞條。
func (c *Compact) All(fn func(Entry)) {
	for i := range c.ent {
		fn(c.entry(&c.ent[i]))
	}
}

const (
	magic   = "ZUINLEX\x00"
	version = 1
)

// Encode 把詞庫寫成資料包格式。
func (c *Compact) Encode() []byte {
	w := binfmt.NewWriter(magic, version)
	var sb strings.Builder
	for _, s := range c.syls {
		sb.WriteString(string(s))
		sb.WriteByte(' ')
	}
	w.String(sb.String())
	w.U16s(c.rd)
	n := len(c.ent)
	rdOff, sfOff := make([]uint32, n), make([]uint32, n)
	sfLen := make([]uint16, n)
	rdLen, flags := make([]uint8, n), make([]uint8, n)
	prw := make([]float32, n)
	for i, e := range c.ent {
		rdOff[i], sfOff[i], sfLen[i], rdLen[i], flags[i], prw[i] = e.rdOff, e.sfOff, e.sfLen, e.rdLen, e.flags, e.prw
	}
	w.U32s(rdOff)
	w.U32s(sfOff)
	w.U16s(sfLen)
	w.Bytes8(rdLen)
	w.Bytes8(flags)
	w.F32s(prw)
	w.String(c.strs)
	w.U32s(c.byText)
	return w.Finish()
}

// Decode 讀回資料包，並檢查所有位移都在範圍內——資料檔可能來自網路下載。
func Decode(b []byte) (*Compact, error) {
	r := binfmt.NewReader(b, magic, version)
	c := &Compact{}
	for _, f := range strings.Fields(r.String()) {
		c.syls = append(c.syls, zhuyin.Syllable(f))
	}
	c.rd = r.U16s()
	rdOff, sfOff, sfLen := r.U32s(), r.U32s(), r.U16s()
	rdLen, flags, prw := r.Bytes8(), r.Bytes8(), r.F32s()
	c.strs = r.String()
	c.byText = r.U32s()
	if err := r.Done(); err != nil {
		return nil, err
	}
	n := len(rdOff)
	if len(sfOff) != n || len(sfLen) != n || len(rdLen) != n || len(flags) != n || len(prw) != n || len(c.byText) != n {
		return nil, errors.New("資料檔各欄長度不一致")
	}
	c.ent = make([]centry, n)
	for i := range c.ent {
		e := centry{rdOff: rdOff[i], sfOff: sfOff[i], sfLen: sfLen[i], rdLen: rdLen[i], flags: flags[i], prw: prw[i]}
		if uint64(e.rdOff)+uint64(e.rdLen) > uint64(len(c.rd)) || uint64(e.sfOff)+uint64(e.sfLen) > uint64(len(c.strs)) || e.rdLen == 0 {
			return nil, fmt.Errorf("詞條 %d 的位移超出範圍", i)
		}
		for _, id := range c.rd[e.rdOff : e.rdOff+uint32(e.rdLen)] {
			if int(id) >= len(c.syls) {
				return nil, fmt.Errorf("詞條 %d 的音節 ID 超出範圍", i)
			}
		}
		if !(e.prw > 0 && e.prw <= 1) {
			return nil, fmt.Errorf("詞條 %d 的 P(r|w) 不在 (0,1]", i)
		}
		c.ent[i] = e
	}
	for _, i := range c.byText {
		if int(i) >= n {
			return nil, errors.New("詞面索引超出範圍")
		}
	}
	c.index()
	c.measure()
	return c, nil
}
