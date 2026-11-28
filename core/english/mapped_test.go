package english

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"
)

// mappedPair 回傳同一份字典的格式 2（Decode）與格式 3（Map）。
func mappedPair(t *testing.T, d *Dictionary) (*Dictionary, *Dictionary, []byte) {
	t.Helper()
	memory, err := Decode(d.Encode())
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.EncodeMapped()
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := Map(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	return memory, mapped, data
}

// sameAnswers 比對兩份字典對 words 與它們的字首、打錯、大小寫、電話鍵的查詢結果。
func sameAnswers(t *testing.T, a, b *Dictionary, words []string) {
	t.Helper()
	if a.Len() != b.Len() || a.Ranked() != b.Ranked() {
		t.Fatalf("Len %d／%d", a.Len(), b.Len())
	}
	check := func(what string, x, y any) {
		t.Helper()
		if !reflect.DeepEqual(x, y) {
			t.Errorf("%s：%v ≠ %v", what, x, y)
		}
	}
	for _, w := range words {
		for _, typed := range []string{w, strings.ToUpper(w), strings.ToUpper(w[:1]) + w[1:], w + "x", w[:len(w)-1]} {
			check("Has "+typed, a.Has(typed), b.Has(typed))
			check("Prior "+typed, a.Prior(typed), b.Prior(typed))
			check("Display "+typed, a.Display(typed), b.Display(typed))
			check("Suggestible "+typed, a.Suggestible(typed), b.Suggestible(typed))
			check("Protected "+typed, a.Protected(typed), b.Protected(typed))
			check("Contractions "+typed, a.Contractions(typed), b.Contractions(typed))
			check("Corrections "+typed, a.Corrections(typed, 8), b.Corrections(typed, 8))
			check("NearCommon "+typed, a.NearCommon(typed, 60), b.NearCommon(typed, 60))
		}
		for i := 1; i <= len(w); i++ {
			check("Prefix "+w[:i], a.Prefix(w[:i], 8), b.Prefix(w[:i], 8))
			check("Prefix upper "+w[:i], a.Prefix(strings.ToUpper(w[:i]), 3), b.Prefix(strings.ToUpper(w[:i]), 3))
		}
		if len(w) > 2 {
			typo := w[1:2] + w[:1] + w[2:]
			check("Corrections "+typo, a.Corrections(typo, 8), b.Corrections(typo, 8))
		}
		check("T9 "+w, a.T9(t9Digits(w), 8), b.T9(t9Digits(w), 8))
	}
}

func t9Digits(w string) string {
	var out []byte
	for i := range len(w) {
		for k, keys := range t9Keys {
			if keys != "" && strings.IndexByte(keys, w[i]) >= 0 {
				out = append(out, byte('0'+k))
			}
		}
	}
	return string(out)
}

func mappedFixture(t *testing.T) (*Dictionary, []string) {
	t.Helper()
	entries := []Entry{
		{Text: "da", Weight: 70, CorpusOnly: true}, {Text: "dan", Weight: 60}, {Text: "data", Weight: 80},
		{Text: "tv", Display: "TV", Weight: 75, CorpusOnly: true}, {Text: "tvbs", Display: "TVBS", Weight: 60, CorpusOnly: true},
		{Text: "gdp", Display: "GDP", Weight: 76, CorpusOnly: true}, {Text: "im", Weight: 50, CorpusOnly: true},
		{Text: "can't", Weight: 78}, {Text: "cant", Weight: 55}, {Text: "y'all", Weight: 50}, {Text: "ya'll", Weight: 40},
		{Text: "shan't", Weight: 41, NoSuggest: true}, {Text: "same", Weight: 70},
	}
	d := rankedFixture(t)
	for i := range d.Len() {
		entries = append(entries, Entry{Text: d.words[i].Text, Weight: d.words[i].Weight, NoSuggest: d.noSuggest[i],
			Protected: d.protected[i], CorpusOnly: d.corpusOnly[i]})
		if c := d.common(i); c != d.words[i].Text {
			entries[len(entries)-1].Display = c
		}
	}
	d, err := NewRanked(entries)
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, e := range entries {
		words = append(words, e.Text)
	}
	return d, append(words, "zz", "q", "helo", "teh", "wich", "dont", "yall", "hellp")
}

func TestMappedAnswersLikeDecoded(t *testing.T) {
	d, words := mappedFixture(t)
	memory, mapped, data := mappedPair(t, d)
	if mapped.col == nil || memory.col != nil {
		t.Fatal("not mapped")
	}
	sameAnswers(t, memory, mapped, words)
	if memory.Mixed() == memory || mapped.Mixed() == mapped || mapped.Mixed().Mixed() != mapped.Mixed() {
		t.Fatal("mixed view")
	}
	sameAnswers(t, memory.Mixed(), mapped.Mixed(), words)
	// 兩種格式互轉逐位元組相同。
	if !bytes.Equal(mapped.Encode(), d.Encode()) {
		t.Fatal("format 3 → 2 differs")
	}
	again, err := mapped.EncodeMapped()
	if err != nil || !bytes.Equal(again, data) {
		t.Fatal("format 3 → 3 differs")
	}
	// Decode 也收格式 3，複製一份。
	copied, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	sameAnswers(t, memory, copied, words)
	// 沒有語料短詞時，Mixed 就是字典本身（同 buildMixed）。
	plain, err := NewRanked([]Entry{{Text: "hello", Weight: 70}, {Text: "help", Weight: 80}})
	if err != nil {
		t.Fatal(err)
	}
	if _, m, _ := mappedPair(t, plain); m.Mixed() != m {
		t.Fatal("mixed view without hidden words")
	}
	if _, err := New([]Word{{"hello", 1}}).EncodeMapped(); err == nil {
		t.Fatal("format 1 dictionaries have no format 3")
	}
}

// resum 改了內容之後重算 binfmt 的 CRC，讓檢查走到內容本身。
func resum(b []byte) []byte {
	body := b[:len(b)-4]
	binary.LittleEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(body))
	return b
}

func TestMapRejectsMalformed(t *testing.T) {
	d, _ := mappedFixture(t)
	data, err := d.EncodeMapped()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Map(data[:len(data)-1], nil); err == nil {
		t.Error("accepted truncated")
	}
	flipped := bytes.Clone(data)
	flipped[40] ^= 1
	if _, err := Map(flipped, nil); err == nil {
		t.Error("accepted bad CRC")
	}
	// 一個個改內容（重算 CRC）：每一種都要被內容檢查擋下。
	offsetOf := func(part []byte) int {
		for i := range data {
			if &data[i] == &part[0] {
				return i
			}
		}
		t.Fatal("not in data")
		return 0
	}
	m, _ := Map(data, nil) // 指向 data，才算得出各欄的位置
	c := m.col
	for name, at := range map[string]int{
		"詞的內容":  len(magicMapped) + 8,                // 第一個詞（版本、長度之後）can't 改成 ban't：trie 走不到
		"權重":    offsetOf(c.weights) + 7,             // 第一個詞的權重的最高位元組：變成負數
		"旗標":    offsetOf(c.flags),                   // 第一個詞加上不存在的旗標
		"寫法的位置": offsetOf(c.displayAt),               // displayAt[0]
		"縮寫":    offsetOf(c.contracted),              // 指到別的詞
		"子節點":   offsetOf(c.nodes) + 1,               // 根的 child
		"兄弟":    offsetOf(c.nodes) + nodeBytes*2 + 4, // 第三個節點的 next
		"詞":     offsetOf(c.nodes) + nodeBytes*3 + 7, // 第四個節點的 word
		"best":  offsetOf(c.nodes) + 10,              // 根的 best
	} {
		bad := bytes.Clone(data)
		switch name {
		case "旗標":
			bad[at] |= 0x80
		case "權重":
			bad[at] |= 0x80
		default:
			bad[at] ^= 0x01
		}
		if _, err := Map(resum(bad), nil); err == nil {
			t.Errorf("%s：改過的內容被接受", name)
		}
	}
	// 節點指標改成越界（大於節點數）：要回錯誤，不能 panic。根的 child、第三個節點的 next，
	// 以及每個節點的 child 與 next 都改一次。
	nodes := offsetOf(c.nodes)
	count := len(c.nodes) / nodeBytes
	for i := 0; i < count; i++ {
		for _, field := range []int{1, 4} { // child、next
			for _, ptr := range []uint32{uint32(count) + 1, maxNode} {
				bad := bytes.Clone(data)
				at := nodes + nodeBytes*i + field
				bad[at], bad[at+1], bad[at+2] = byte(ptr), byte(ptr>>8), byte(ptr>>16)
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("節點 %d 的欄位 %d 改成 %d：panic %v", i+1, field, ptr, r)
						}
					}()
					if _, err := Map(resum(bad), nil); err == nil {
						t.Fatalf("節點 %d 的欄位 %d 改成越界的 %d 被接受", i+1, field, ptr)
					}
				}()
			}
		}
	}
	// 逐位元組改（每個位元組加 1），只要求不 panic：有些改法剛好還是合法的資料。
	for at := 0; at < len(data)-4; at++ {
		bad := bytes.Clone(data)
		bad[at]++
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("第 %d 個位元組改過：panic %v", at, r)
				}
			}()
			_, _ = Map(resum(bad), nil)
		}()
	}
}

func TestMappedBigramsAnswerLikeDecoded(t *testing.T) {
	b, err := NewBigrams([3][]Pair{
		EnglishEnglish: {{"of", "the", 30}, {"of", "a", 6}, {"in", "the", 29}, {"don't", "know", 9}, {"of", "an", 6}},
		ChineseEnglish: {{"笑死", "xd", 83}, {"我", "ok", 80}},
		EnglishChinese: {{"ai", "泡沫", 101}, {"ai", "晶片", 50}},
	})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := DecodeBigrams(b.Encode())
	if err != nil {
		t.Fatal(err)
	}
	data := b.EncodeMapped()
	mapped, err := MapBigrams(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Len() != memory.Len() || !bytes.Equal(mapped.Encode(), b.Encode()) || !bytes.Equal(mapped.EncodeMapped(), data) {
		t.Fatal("round trip")
	}
	for kind := range bigramKinds {
		for _, left := range []string{"of", "OF", "in", "don't", "笑死", "我", "ai", "AI", "zz", ""} {
			for _, limit := range []int{0, 1, 2, 10} {
				if x, y := memory.After(kind, left, limit), mapped.After(kind, left, limit); !reflect.DeepEqual(x, y) {
					t.Errorf("After(%d, %s, %d)：%v ≠ %v", kind, left, limit, x, y)
				}
			}
			for _, right := range []string{"the", "a", "an", "know", "xd", "XD", "ok", "泡沫", "晶片", "zz"} {
				if x, y := memory.Count(kind, left, right), mapped.Count(kind, left, right); x != y {
					t.Errorf("Count(%d, %s, %s)：%d ≠ %d", kind, left, right, x, y)
				}
			}
		}
	}
	if _, err := MapBigrams(data[:len(data)-1], nil); err == nil {
		t.Error("accepted truncated")
	}
	// 次數的順序（of 的後詞 the 30、a 6、an 6）：把 a 的次數改大，順序就錯了。
	c := mapped.tables[EnglishEnglish].col
	for i := range data {
		if &data[i] == &c.count[12] { // 第四筆（of a）的次數
			bad := bytes.Clone(data)
			bad[i] = 200
			if _, err := MapBigrams(resum(bad), nil); err == nil {
				t.Error("accepted unordered counts")
			}
		}
	}
	var empty Bigrams
	if back, err := MapBigrams(empty.EncodeMapped(), nil); err != nil || back.Len() != 0 || back.After(EnglishEnglish, "of", 3) != nil {
		t.Fatalf("empty: %v", err)
	}
}
