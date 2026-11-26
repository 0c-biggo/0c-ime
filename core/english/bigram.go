package english

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Bigrams 是英文詞對與跨中英詞對的語料次數（APP#21），建好後唯讀，可在 session 之間共用。
// 英文端是字典的小寫鍵（顯示寫法查 Dictionary.Display），中文端是中文詞庫的詞。次數是剪枝後
// 留下的，只拿來比大小，不是完整的條件機率。
type Bigrams struct {
	tables [bigramKinds]bigramTable // 英文 → 英文、中文詞 → 英文、英文 → 中文詞
	pairs  int
}

// bigramTable 是排好序的前詞，每個前詞的後詞在 next[start[i]:start[i+1]]，次數多的在前。
// 攤平存放，不用 map，常駐記憶體少一半以上。
type bigramTable struct {
	lefts []string
	start []uint32
	next  []Next
}

func (t *bigramTable) after(left string) []Next {
	i, ok := slices.BinarySearch(t.lefts, left)
	if !ok {
		return nil
	}
	return t.next[t.start[i]:t.start[i+1]]
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
	next := b.tables[kind].after(left)
	return slices.Clone(next[:min(limit, len(next))])
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
	for _, n := range b.tables[kind].after(left) {
		if n.Text == right {
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
		binary.Write(&out, binary.LittleEndian, uint32(len(t.lefts)))
		for i, left := range t.lefts {
			next := t.next[t.start[i]:t.start[i+1]]
			out.WriteByte(byte(len(left)))
			out.WriteString(left)
			binary.Write(&out, binary.LittleEndian, uint16(len(next)))
			for _, n := range next {
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
