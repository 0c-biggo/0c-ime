package lexicon

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/0c-biggo/0c-ime/core/binfmt"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Compact 是緊湊的唯讀詞庫。詞條依讀音（音節 ID 序列）排序，讀音以某前綴開頭的詞條
// 在陣列中是連續的一段，所以精確查詢與前綴查詢都是二分搜尋。沒有指標、沒有每節點的 map，
// 可以原樣寫進資料包（docs/19 的執行期資料）。
//
// 各欄直接指向資料包的位元組（小端序），載入時不複製：手機把資料包 mmap 進來，
// 詞庫就不佔 Go heap（iOS 鍵盤的記憶體上限只算 heap 這類 dirty 頁，乾淨的檔案頁不算）。
type Compact struct {
	syls []zhuyin.Syllable // 音節 ID → 音節，依字串排序
	ids  map[zhuyin.Syllable]uint16
	n    int    // 詞條數
	nrd  uint32 // rd 的音節數
	// first[y] 是第一個首音節 ≥ y 的詞條（len = 音節數+1）。載入時建（約 6 KB、在 heap），
	// 二分搜尋只在同一個首音節的那一段裡找，不必每次走過整個詞庫。
	first []uint32
	// 以下每欄 n 個元素（rd 除外），依讀音排序；寬度見 Encode。
	rd           []byte // 所有讀音串接，uint16
	rdOff, sfOff []byte // uint32
	sfLen        []byte // uint16
	rdLen, flags []byte // uint8
	prw          []byte // float32
	strs         string // 所有詞面串接
	byText       []byte // uint32：詞條索引，依詞面排序，供 Has
	// longest 是最長的詞條讀音音節數。由詞條算出、不寫進資料檔，所以舊資料包也有值、格式不變（#142）。
	longest int
	// next 記住 Continuations 查過的前綴；同一份詞庫會被多個 session 同時讀，所以加鎖。
	nextMu sync.Mutex
	next   map[string][]uint16
}

func u16(b []byte, i int) uint16 { return binary.LittleEndian.Uint16(b[2*i:]) }
func u32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[4*i:]) }

// centry 是 Build 時的詞條；寫進資料包後分成各欄。
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
	var syls []zhuyin.Syllable
	for s := range set {
		syls = append(syls, s)
	}
	sort.Slice(syls, func(i, j int) bool { return syls[i] < syls[j] })
	ids := make(map[zhuyin.Syllable]uint16, len(syls))
	for i, s := range syls {
		ids[s] = uint16(i)
	}

	type keyed struct {
		rd []uint16
		e  Entry
	}
	ks := make([]keyed, 0, len(entries))
	for _, e := range entries {
		rd := make([]uint16, len(e.Reading))
		for i, s := range e.Reading {
			rd[i] = ids[s]
		}
		ks = append(ks, keyed{rd, e})
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if d := cmpIDs(ks[i].rd, ks[j].rd); d != 0 {
			return d < 0
		}
		return ks[i].e.Surface < ks[j].e.Surface
	})
	var (
		rd   []uint16
		ent  []centry
		strs strings.Builder
	)
	surfaceOf := func(e *centry) string { return strs.String()[e.sfOff : e.sfOff+uint32(e.sfLen)] }
	seen := map[string]int{} // 詞面 → 在 strs 的位移
	for _, k := range ks {
		if n := len(ent); n > 0 {
			last := &ent[n-1]
			if cmpIDs(rd[last.rdOff:last.rdOff+uint32(last.rdLen)], k.rd) == 0 && surfaceOf(last) == k.e.Surface {
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
		ent = append(ent, centry{
			rdOff: uint32(len(rd)), rdLen: uint8(len(k.rd)),
			sfOff: uint32(off), sfLen: uint16(len(k.e.Surface)),
			prw: float32(k.e.PRW), flags: flagsOf(k.e),
		})
		rd = append(rd, k.rd...)
	}
	// 同一個詞面有多個讀音時以儲存順序（依讀音排序）決勝。只比詞面的話，同詞面之間的順序由
	// sort.Slice 的內部實作決定，換一版 Go 就不同：Lookup 的順序、lexicon.bin 的內容、
	// 以及「同分取第一個」的標音結果（0c-import 的路名讀音）都會跟著變。
	byText := make([]uint32, len(ent))
	for i := range byText {
		byText[i] = uint32(i)
	}
	sort.Slice(byText, func(i, j int) bool {
		si, sj := surfaceOf(&ent[byText[i]]), surfaceOf(&ent[byText[j]])
		if si != sj {
			return si < sj
		}
		return byText[i] < byText[j]
	})

	w := binfmt.NewWriter(magic, version)
	w.String(sylString(syls))
	w.U16s(rd)
	n := len(ent)
	rdOff, sfOff := make([]uint32, n), make([]uint32, n)
	sfLen := make([]uint16, n)
	rdLen, flags := make([]uint8, n), make([]uint8, n)
	prw := make([]float32, n)
	for i, e := range ent {
		rdOff[i], sfOff[i], sfLen[i], rdLen[i], flags[i], prw[i] = e.rdOff, e.sfOff, e.sfLen, e.rdLen, e.flags, e.prw
	}
	w.U32s(rdOff)
	w.U32s(sfOff)
	w.U16s(sfLen)
	w.Bytes8(rdLen)
	w.Bytes8(flags)
	w.F32s(prw)
	w.String(strs.String())
	w.U32s(byText)
	return Decode(w.Finish())
}

func sylString(syls []zhuyin.Syllable) string {
	var sb strings.Builder
	for _, s := range syls {
		sb.WriteString(string(s))
		sb.WriteByte(' ')
	}
	return sb.String()
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
	for _, n := range c.rdLen {
		c.longest = max(c.longest, int(n))
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

// span 回傳第 i 個詞條的讀音在 rd 的起點與音節數。讀音依詞條順序連續存放（Map 檢查過），
// 所以長度由下一個詞條的起點算出：二分搜尋的每一步只碰 rdOff 與 rd，不必再讀 rdLen 那一欄。
func (c *Compact) span(i int) (int, int) {
	off := u32(c.rdOff, i)
	end := c.nrd
	if i+1 < c.n {
		end = u32(c.rdOff, i+1)
	}
	return int(off), int(end - off)
}

// cmpReading 比較第 i 個詞條的讀音與 q，規則同 cmpIDs。
func (c *Compact) cmpReading(i int, q []uint16) int {
	off, n := c.span(i)
	r := c.rd[2*off : 2*(off+n)]
	for k := 0; k < n && k < len(q); k++ {
		if a := u16(r, k); a != q[k] {
			if a < q[k] {
				return -1
			}
			return 1
		}
	}
	return n - len(q)
}

// cmpHead 只比較第 i 個詞條讀音的前 len(q) 個音節：讀音以 q 開頭時回傳 0。
func (c *Compact) cmpHead(i int, q []uint16) int {
	off, n := c.span(i)
	r := c.rd[2*off : 2*(off+n)]
	for k := 0; k < n && k < len(q); k++ {
		if a := u16(r, k); a != q[k] {
			if a < q[k] {
				return -1
			}
			return 1
		}
	}
	if n < len(q) {
		return -1
	}
	return 0
}

// hasPrefix 回報第 i 個詞條的讀音是否以 q 開頭。
func (c *Compact) hasPrefix(i int, q []uint16) bool {
	off, n := c.span(i)
	if n < len(q) {
		return false
	}
	r := c.rd[2*off : 2*(off+len(q))]
	for k, id := range q {
		if u16(r, k) != id {
			return false
		}
	}
	return true
}

func (c *Compact) surface(i int) string {
	off := u32(c.sfOff, i)
	return c.strs[off : off+uint32(u16(c.sfLen, i))]
}

func (c *Compact) entry(i int) Entry {
	off, n := c.span(i)
	r := make([]zhuyin.Syllable, n)
	for k := range r {
		r[k] = c.syls[u16(c.rd, off+k)]
	}
	f := c.flags[i]
	return Entry{Surface: c.surface(i), Reading: r, PRW: float64(math.Float32frombits(u32(c.prw, i))), Correction: f&flagCorrection != 0, Rare: f&flagRare != 0}
}

// block 回傳首音節是 q[0] 的詞條範圍；讀音 ≥ q 的第一個詞條一定落在 [lo, hi]。
func (c *Compact) block(q []uint16) (int, int) {
	if len(q) == 0 {
		return 0, c.n
	}
	return int(c.first[q[0]]), int(c.first[q[0]+1])
}

// lower 回傳第一個讀音 ≥ q 的詞條位置。
func (c *Compact) lower(q []uint16) int {
	lo, hi := c.block(q)
	return lo + sort.Search(hi-lo, func(k int) bool { return c.cmpReading(lo+k, q) >= 0 })
}

func (c *Compact) Exact(syls []zhuyin.Syllable) []Entry {
	q, ok := c.lookupIDs(syls)
	if !ok {
		return nil
	}
	var out []Entry
	for i := c.lower(q); i < c.n && c.cmpReading(i, q) == 0; i++ {
		out = append(out, c.entry(i))
	}
	return out
}

func (c *Compact) Prefixed(syls []zhuyin.Syllable) []Entry {
	q, ok := c.lookupIDs(syls)
	if !ok {
		return nil
	}
	var out []Entry
	for i := c.lower(q); i < c.n && c.hasPrefix(i, q); i++ {
		out = append(out, c.entry(i))
	}
	return out
}

func (c *Compact) HasPrefix(syls []zhuyin.Syllable) bool {
	q, ok := c.lookupIDs(syls)
	if !ok {
		return false
	}
	i := c.lower(q)
	return i < c.n && c.hasPrefix(i, q)
}

// Continuations 依讀音排序跳著走：每個不同的下一個音節只做一次二分搜尋。詞庫不會變，結果記在 next 裡：
// 每打一鍵整段重新解碼，同樣的前綴會一再查到。
func (c *Compact) Continuations(prefix []zhuyin.Syllable, add func(zhuyin.Syllable)) bool {
	q, ok := c.lookupIDs(prefix)
	if !ok {
		return true
	}
	for _, id := range c.continuations(q) {
		add(c.syls[id])
	}
	return true
}

// maxContinuationCache 是 Continuations 記住的前綴數上限；超過就全部清掉重記（打字時的前綴會很快再查到）。
const maxContinuationCache = 4096

func (c *Compact) continuations(q []uint16) []uint16 {
	key := idsKey(q)
	c.nextMu.Lock()
	ids, ok := c.next[key]
	c.nextMu.Unlock()
	if ok {
		return ids
	}
	ids = c.scanContinuations(q)
	c.nextMu.Lock()
	if c.next == nil || len(c.next) >= maxContinuationCache {
		c.next = map[string][]uint16{}
	}
	c.next[key] = ids
	c.nextMu.Unlock()
	return ids
}

func idsKey(q []uint16) string {
	b := make([]byte, 2*len(q))
	for i, id := range q {
		b[2*i], b[2*i+1] = byte(id>>8), byte(id)
	}
	return string(b)
}

// scanContinuations 在讀音以 q 開頭的那一段裡跳著走，每次二分搜尋都只找這一段。
func (c *Compact) scanContinuations(q []uint16) []uint16 {
	var out []uint16
	lo := c.lower(q)
	_, end := c.block(q)
	hi := lo + sort.Search(end-lo, func(k int) bool { return c.cmpHead(lo+k, q) > 0 })
	next := append(q[:len(q):len(q)], 0)
	for i := lo; i < hi; {
		off, n := c.span(i)
		if n == len(q) {
			i++
			continue
		}
		y := u16(c.rd, off+len(q))
		out = append(out, y)
		if y == ^uint16(0) {
			break
		}
		next[len(q)] = y + 1
		i += sort.Search(hi-i, func(k int) bool { return c.cmpReading(i+k, next) >= 0 })
	}
	return out
}

// firstText 回傳 byText 裡第一個詞面 ≥ surface 的位置。
func (c *Compact) firstText(surface string) int {
	return sort.Search(c.n, func(k int) bool { return c.surface(int(u32(c.byText, k))) >= surface })
}

func (c *Compact) Has(surface string) bool {
	k := c.firstText(surface)
	return k < c.n && c.surface(int(u32(c.byText, k))) == surface
}

// Lookup 回傳某詞面的所有讀音。
func (c *Compact) Lookup(surface string) []Entry {
	var out []Entry
	for k := c.firstText(surface); k < c.n; k++ {
		i := int(u32(c.byText, k))
		if c.surface(i) != surface {
			break
		}
		out = append(out, c.entry(i))
	}
	return out
}

// Len 回傳詞條數。
func (c *Compact) Len() int { return c.n }

// All 依儲存順序走訪所有詞條。
func (c *Compact) All(fn func(Entry)) {
	for i := range c.n {
		fn(c.entry(i))
	}
}

const (
	magic   = "ZUINLEX\x00"
	version = 1
)

// Encode 把詞庫寫成資料包格式：音節表（以空白分隔的字串）、讀音（uint16）、各詞條的
// 讀音位移（uint32）、詞面位移（uint32）、詞面長度（uint16）、讀音長度（uint8）、旗標（uint8）、
// P(r|w)（float32）、詞面串接、依詞面排序的詞條索引（uint32）。
func (c *Compact) Encode() []byte {
	w := binfmt.NewWriter(magic, version)
	w.String(sylString(c.syls))
	w.Raw(len(c.rd)/2, c.rd)
	w.Raw(c.n, c.rdOff)
	w.Raw(c.n, c.sfOff)
	w.Raw(c.n, c.sfLen)
	w.Raw(c.n, c.rdLen)
	w.Raw(c.n, c.flags)
	w.Raw(c.n, c.prw)
	w.String(c.strs)
	w.Raw(c.n, c.byText)
	return w.Finish()
}

// Decode 讀回資料包，並檢查所有位移都在範圍內——資料檔可能來自網路下載。
// 詞庫直接指向 b（見 Map），b 之後不能再改；詞面串接複製成字串。
func Decode(b []byte) (*Compact, error) { return Map(b, nil) }

// Map 同 Decode，但連詞面串接也交給 str 轉成字串：宿主把資料包 mmap 進來、傳入不複製的轉換
// （unsafe.String；core 不用 unsafe），整份詞庫就都留在檔案頁裡。str 為 nil 時複製。
// b 在詞庫的整個生命期內不能改，也不能 munmap。
func Map(b []byte, str func([]byte) string) (*Compact, error) {
	if str == nil {
		str = func(b []byte) string { return string(b) }
	}
	r := binfmt.NewReader(b, magic, version)
	c := &Compact{}
	for _, f := range strings.Fields(r.String()) {
		c.syls = append(c.syls, zhuyin.Syllable(f))
	}
	c.rd = r.View(2)
	c.rdOff, c.sfOff, c.sfLen = r.View(4), r.View(4), r.View(2)
	c.rdLen, c.flags, c.prw = r.View(1), r.View(1), r.View(4)
	c.strs = str(r.View(1))
	c.byText = r.View(4)
	if err := r.Done(); err != nil {
		return nil, err
	}
	n := len(c.rdLen)
	c.n = n
	if len(c.rdOff) != 4*n || len(c.sfOff) != 4*n || len(c.sfLen) != 2*n || len(c.flags) != n || len(c.prw) != 4*n || len(c.byText) != 4*n {
		return nil, errors.New("資料檔各欄長度不一致")
	}
	if len(c.rd)/2 > math.MaxUint32 {
		return nil, errors.New("讀音過多")
	}
	c.nrd = uint32(len(c.rd) / 2)
	var at uint64 // 讀音依詞條順序連續存放（Build 就是這樣寫的；span 依賴這點）
	for i := range n {
		off, l := uint64(u32(c.rdOff, i)), uint64(c.rdLen[i])
		if off != at || off+l > uint64(c.nrd) || uint64(u32(c.sfOff, i))+uint64(u16(c.sfLen, i)) > uint64(len(c.strs)) || l == 0 {
			return nil, fmt.Errorf("詞條 %d 的位移超出範圍", i)
		}
		at = off + l
		for k := range int(l) {
			if int(u16(c.rd, int(off)+k)) >= len(c.syls) {
				return nil, fmt.Errorf("詞條 %d 的音節 ID 超出範圍", i)
			}
		}
		if p := math.Float32frombits(u32(c.prw, i)); !(p > 0 && p <= 1) {
			return nil, fmt.Errorf("詞條 %d 的 P(r|w) 不在 (0,1]", i)
		}
	}
	if at != uint64(c.nrd) {
		return nil, errors.New("讀音區段有多餘的音節")
	}
	for k := range n {
		if int(u32(c.byText, k)) >= n {
			return nil, errors.New("詞面索引超出範圍")
		}
	}
	c.index()
	c.measure()
	c.first = make([]uint32, len(c.syls)+1)
	y := 0
	for i := range n {
		for at := int(u16(c.rd, int(u32(c.rdOff, i)))); y <= at; y++ {
			c.first[y] = uint32(i)
		}
	}
	for ; y <= len(c.syls); y++ {
		c.first[y] = uint32(n)
	}
	return c, nil
}
