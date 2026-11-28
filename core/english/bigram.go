package english

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/binfmt"
)

// Bigrams 是英文詞對與跨中英詞對的語料次數（APP#21），建好後唯讀，可在 session 之間共用。
// 英文端是字典的小寫鍵（顯示寫法查 Dictionary.Display），中文端是中文詞庫的詞。次數是剪枝後
// 留下的，只拿來比大小，不是完整的條件機率。
type Bigrams struct {
	tables [bigramKinds]bigramTable // 英文 → 英文、中文詞 → 英文、英文 → 中文詞
	pairs  int
}

// bigramTable 是排好序的前詞，每個前詞的後詞在 next[start[i]:start[i+1]]，次數多的在前。
// 攤平存放，不用 map，常駐記憶體少一半以上。col 不是 nil 的是格式 2（MapBigrams）：同樣的排列
// 直接讀資料包的位元組，上面三個 slice 是空的。
type bigramTable struct {
	lefts []string
	start []uint32
	next  []Next
	col   *bigramColumns
}

func (t *bigramTable) leftCount() int {
	if t.col != nil {
		return t.col.lefts
	}
	return len(t.lefts)
}

func (t *bigramTable) left(i int) string {
	if t.col != nil {
		return t.col.leftText[u32At(t.col.leftOff, i):u32At(t.col.leftOff, i+1)]
	}
	return t.lefts[i]
}

func (t *bigramTable) startAt(i int) int {
	if t.col != nil {
		return int(u32At(t.col.start, i))
	}
	return int(t.start[i])
}

func (t *bigramTable) at(j int) Next {
	if t.col != nil {
		c := t.col
		return Next{c.rightText[u32At(c.rightOff, j):u32At(c.rightOff, j+1)], u32At(c.count, j)}
	}
	return t.next[j]
}

func (t *bigramTable) pairCount() int {
	if t.col != nil {
		return t.col.pairs
	}
	return len(t.next)
}

// span 是 left 的後詞在 [lo, hi)；沒有這個前詞時 ok 是 false。
func (t *bigramTable) span(left string) (lo, hi int, ok bool) {
	n := t.leftCount()
	a, b := 0, n
	for a < b {
		mid := int(uint(a+b) >> 1)
		if t.left(mid) < left {
			a = mid + 1
		} else {
			b = mid
		}
	}
	if a == n || t.left(a) != left {
		return 0, 0, false
	}
	return t.startAt(a), t.startAt(a + 1), true
}

// Next 是接在某個詞後面的詞與次數。
type Next struct {
	Text  string
	Count uint32
}

// Pair 是一筆詞對，用來建 Bigrams。
type Pair struct {
	Left, Right string
	Count       uint32
}

// BigramKind 是詞對的種類。
type BigramKind int

const (
	EnglishEnglish BigramKind = iota // 英文 → 英文
	ChineseEnglish                   // 中文 → 英文（笑死 → xd）
	EnglishChinese                   // 英文 → 中文（ai → 泡沫）
	bigramKinds
)

const MaxBigramPairs = 1 << 20

// NewBigrams 建立詞對表。同一種類的同一對只能出現一次；英文端須是小寫鍵（可含撇號），中文端
// 須是不含 ASCII 的 UTF-8。
func NewBigrams(pairs [bigramKinds][]Pair) (*Bigrams, error) {
	b := &Bigrams{}
	for kind, list := range pairs {
		groups := map[string][]Next{}
		for _, p := range list {
			if !validPair(BigramKind(kind), p.Left, p.Right) || p.Count == 0 {
				return nil, fmt.Errorf("英文詞對不合法：%q %q", p.Left, p.Right)
			}
			groups[p.Left] = append(groups[p.Left], Next{p.Right, p.Count})
		}
		t := &b.tables[kind]
		for left := range groups {
			t.lefts = append(t.lefts, left)
		}
		slices.Sort(t.lefts)
		t.next = make([]Next, 0, len(list))
		for _, left := range t.lefts {
			next := groups[left]
			slices.SortFunc(next, compareNext)
			for i := 1; i < len(next); i++ {
				if next[i].Text == next[i-1].Text {
					return nil, fmt.Errorf("英文詞對重複：%q %q", left, next[i].Text)
				}
			}
			t.start = append(t.start, uint32(len(t.next)))
			t.next = append(t.next, next...)
		}
		t.start = append(t.start, uint32(len(t.next)))
		b.pairs += len(t.next)
	}
	if b.pairs > MaxBigramPairs {
		return nil, fmt.Errorf("英文詞對過多")
	}
	return b, nil
}

func validPair(kind BigramKind, left, right string) bool {
	switch kind {
	case EnglishEnglish:
		return rankedKey(left) && rankedKey(right)
	case ChineseEnglish:
		return chineseWord(left) && rankedKey(right)
	case EnglishChinese:
		return rankedKey(left) && chineseWord(right)
	}
	return false
}

func compareNext(a, b Next) int {
	if a.Count != b.Count {
		if a.Count > b.Count {
			return -1
		}
		return 1
	}
	return strings.Compare(a.Text, b.Text)
}

func chineseWord(s string) bool {
	if s == "" || len(s) > 64 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x80 || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// After 是接在 left 後面的詞，次數多的在前，最多 limit 個。英文的 left 不分大小寫。
func (b *Bigrams) After(kind BigramKind, left string, limit int) []Next {
	if b == nil || limit <= 0 {
		return nil
	}
	if kind != ChineseEnglish {
		left = strings.ToLower(left)
	}
	if kind < 0 || kind >= bigramKinds {
		return nil
	}
	t := &b.tables[kind]
	lo, hi, ok := t.span(left)
	if !ok {
		return nil
	}
	out := make([]Next, 0, min(limit, hi-lo))
	for j := lo; j < hi && len(out) < limit; j++ {
		out = append(out, t.at(j))
	}
	return out
}

// Count 是 left 接 right 的次數；沒有這一對是 0。
func (b *Bigrams) Count(kind BigramKind, left, right string) uint32 {
	if b == nil || kind < 0 || kind >= bigramKinds {
		return 0
	}
	if kind != ChineseEnglish {
		left = strings.ToLower(left)
	}
	if kind != EnglishChinese {
		right = strings.ToLower(right)
	}
	t := &b.tables[kind]
	lo, hi, _ := t.span(left)
	for j := lo; j < hi; j++ {
		if n := t.at(j); n.Text == right {
			return n.Count
		}
	}
	return 0
}

// Len 是詞對總數。
func (b *Bigrams) Len() int {
	if b == nil {
		return 0
	}
	return b.pairs
}

var magicBigrams = []byte("0cEB\x00\x01")

// Encode：三種詞對依序各一段；每段是前詞數，每個前詞接著它的後詞（次數多的在前）。
// 前詞照字典序，資料相同就產生相同位元組。
func (b *Bigrams) Encode() []byte {
	var out bytes.Buffer
	out.Write(magicBigrams)
	for kind := range bigramKinds {
		t := &b.tables[kind]
		binary.Write(&out, binary.LittleEndian, uint32(t.leftCount()))
		for i := range t.leftCount() {
			left, lo, hi := t.left(i), t.startAt(i), t.startAt(i+1)
			out.WriteByte(byte(len(left)))
			out.WriteString(left)
			binary.Write(&out, binary.LittleEndian, uint16(hi-lo))
			for j := lo; j < hi; j++ {
				n := t.at(j)
				out.WriteByte(byte(len(n.Text)))
				out.WriteString(n.Text)
				binary.Write(&out, binary.LittleEndian, n.Count)
			}
		}
	}
	return out.Bytes()
}

// DecodeBigrams 讀回 Encode 的內容，逐筆檢查詞形與順序。所有詞共用一份字串，不逐詞配置。
func DecodeBigrams(data []byte) (*Bigrams, error) {
	if len(data) > MaxPackBytes || !bytes.HasPrefix(data, magicBigrams) {
		return nil, fmt.Errorf("英文詞對資料標頭或大小不合法")
	}
	text := string(data)
	at := len(magicBigrams)
	fail := fmt.Errorf("英文詞對資料截斷或不合法")
	word := func() (string, bool) {
		if at >= len(text) {
			return "", false
		}
		n := int(text[at])
		if n == 0 || at+1+n > len(text) {
			return "", false
		}
		w := text[at+1 : at+1+n]
		at += 1 + n
		return w, true
	}
	b := &Bigrams{}
	for kind := range bigramKinds {
		if at+4 > len(text) {
			return nil, fail
		}
		lefts := int(binary.LittleEndian.Uint32(data[at:]))
		at += 4
		if lefts > MaxBigramPairs || lefts > len(text) {
			return nil, fail
		}
		t := &b.tables[kind]
		t.lefts = make([]string, 0, lefts)
		t.start = make([]uint32, 0, lefts+1)
		for range lefts {
			left, ok := word()
			if !ok || len(t.lefts) > 0 && left <= t.lefts[len(t.lefts)-1] || at+2 > len(text) {
				return nil, fail
			}
			n := int(binary.LittleEndian.Uint16(data[at:]))
			at += 2
			if n == 0 {
				return nil, fail
			}
			t.lefts = append(t.lefts, left)
			t.start = append(t.start, uint32(len(t.next)))
			for i := range n {
				right, ok := word()
				if !ok || at+4 > len(text) || !validPair(kind, left, right) {
					return nil, fail
				}
				next := Next{right, binary.LittleEndian.Uint32(data[at:])}
				at += 4
				if next.Count == 0 || i > 0 && compareNext(t.next[len(t.next)-1], next) >= 0 {
					return nil, fail
				}
				t.next = append(t.next, next)
				if b.pairs+len(t.next) > MaxBigramPairs {
					return nil, fail
				}
			}
		}
		t.start = append(t.start, uint32(len(t.next)))
		t.next = slices.Clone(t.next) // 只留剛好的容量
		b.pairs += len(t.next)
	}
	if at != len(text) {
		return nil, fmt.Errorf("英文詞對資料有多餘內容")
	}
	return b, nil
}

// 詞對的格式 2（APP 中英混打的記憶體，同字典的格式 3）：同一份詞對排成固定寬度的欄，Map 進來直接
// 二分搜尋，不解成 Go 的字串與 slice。內容與格式 1 一一對應（EncodeMapped、Encode 互轉逐位元組相同）。
// 資料包的 bigram.bin 仍是格式 1。三種詞對依序各一組欄（小端序，binfmt）：
//
//	leftText   前詞串接，依字典序
//	leftOff    uint32 × (前詞數+1)
//	start      uint32 × (前詞數+1)，前詞 i 的後詞是 [start[i], start[i+1])
//	rightText  後詞串接，每個前詞的後詞次數多的在前
//	rightOff   uint32 × (詞對數+1)
//	count      uint32 × 詞對數
var magicBigramsMapped = []byte("0cEB\x00\x02")

type bigramColumns struct {
	leftText, rightText             string
	leftOff, start, rightOff, count []byte
	lefts, pairs                    int
}

// EncodeMapped 把詞對寫成格式 2（見上）。
func (b *Bigrams) EncodeMapped() []byte {
	w := binfmt.NewWriter(string(magicBigramsMapped), mappedVersion)
	for kind := range bigramKinds {
		t := &b.tables[kind]
		var lefts, rights strings.Builder
		var leftOff, start, rightOff, count []byte
		for i := range t.leftCount() {
			leftOff = binary.LittleEndian.AppendUint32(leftOff, uint32(lefts.Len()))
			lefts.WriteString(t.left(i))
			start = binary.LittleEndian.AppendUint32(start, uint32(t.startAt(i)))
		}
		leftOff = binary.LittleEndian.AppendUint32(leftOff, uint32(lefts.Len()))
		start = binary.LittleEndian.AppendUint32(start, uint32(t.pairCount()))
		for j := range t.pairCount() {
			n := t.at(j)
			rightOff = binary.LittleEndian.AppendUint32(rightOff, uint32(rights.Len()))
			rights.WriteString(n.Text)
			count = binary.LittleEndian.AppendUint32(count, n.Count)
		}
		rightOff = binary.LittleEndian.AppendUint32(rightOff, uint32(rights.Len()))
		w.String(lefts.String())
		w.Raw(len(leftOff)/4, leftOff)
		w.Raw(len(start)/4, start)
		w.String(rights.String())
		w.Raw(len(rightOff)/4, rightOff)
		w.Raw(len(count)/4, count)
	}
	return w.Finish()
}

// MapBigrams 讀詞對的格式 2，不複製、不解碼；str 同 Map。b 在詞對表的整個生命期內不能改，也不能
// munmap。逐筆檢查詞形與順序，跟 DecodeBigrams 收的內容相同。
func MapBigrams(b []byte, str func([]byte) string) (*Bigrams, error) {
	if len(b) > MaxPackBytes {
		return nil, fmt.Errorf("英文詞對資料標頭或大小不合法")
	}
	if str == nil {
		str = func(b []byte) string { return string(b) }
	}
	r := binfmt.NewReader(b, string(magicBigramsMapped), mappedVersion)
	type raw struct{ leftText, leftOff, start, rightText, rightOff, count []byte }
	var cols [bigramKinds]raw
	for kind := range bigramKinds {
		c := &cols[kind]
		c.leftText, c.leftOff, c.start = r.View(1), r.View(4), r.View(4)
		c.rightText, c.rightOff, c.count = r.View(1), r.View(4), r.View(4)
	}
	if err := r.Done(); err != nil {
		return nil, err
	}
	fail := fmt.Errorf("英文詞對資料截斷或不合法")
	out := &Bigrams{}
	for kind := range bigramKinds {
		c := cols[kind]
		lefts, pairs := len(c.leftOff)/4-1, len(c.count)/4
		if lefts < 0 || len(c.start) != len(c.leftOff) || len(c.rightOff) != 4*(pairs+1) || out.pairs+pairs > MaxBigramPairs {
			return nil, fail
		}
		t := &bigramColumns{leftText: str(c.leftText), rightText: str(c.rightText), leftOff: c.leftOff, start: c.start,
			rightOff: c.rightOff, count: c.count, lefts: lefts, pairs: pairs}
		if !offsets(t.leftOff, lefts, len(t.leftText)) || !offsets(t.rightOff, pairs, len(t.rightText)) ||
			u32At(t.start, 0) != 0 || int(u32At(t.start, lefts)) != pairs {
			return nil, fail
		}
		table := bigramTable{col: t}
		for i := range lefts {
			left, lo, hi := table.left(i), table.startAt(i), table.startAt(i+1)
			if len(left) > 255 || i > 0 && left <= table.left(i-1) || hi <= lo || hi > pairs || hi-lo > 0xffff {
				return nil, fail
			}
			for j := lo; j < hi; j++ {
				n := table.at(j)
				if len(n.Text) > 255 || !validPair(BigramKind(kind), left, n.Text) || n.Count == 0 || j > lo && compareNext(table.at(j-1), n) >= 0 {
					return nil, fail
				}
			}
		}
		out.tables[kind] = table
		out.pairs += pairs
	}
	return out, nil
}

// offsets：n+1 個遞增的位移，從 0 到 size，每段至少一個位元組。
func offsets(off []byte, n, size int) bool {
	if len(off) != 4*(n+1) || u32At(off, 0) != 0 || int(u32At(off, n)) != size {
		return false
	}
	for i := range n {
		if u32At(off, i+1) <= u32At(off, i) {
			return false
		}
	}
	return true
}
