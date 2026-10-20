package lexicon

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func toyEntries(t *testing.T) []Entry {
	f, err := os.Open("../../testdata/toy/lexicon.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []Entry
	m.Walk(func(e Entry) { out = append(out, e) })
	return out
}

// norm 排序並把 P(r|w) 取到 float32 精度（資料包以 float32 儲存）。
func norm(es []Entry) []Entry {
	out := append([]Entry(nil), es...)
	for i := range out {
		out[i].PRW = float64(float32(out[i].PRW))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Surface != out[j].Surface {
			return out[i].Surface < out[j].Surface
		}
		return zhuyin.Key(out[i].Reading) < zhuyin.Key(out[j].Reading)
	})
	return out
}

// Compact 對所有查詢的結果必須與參考實作 Mem 相同（順序除外）；編碼、解碼後亦同。
func TestCompactMatchesMem(t *testing.T) {
	es := toyEntries(t)
	mem := NewMem()
	for _, e := range es {
		mem.Add(e)
	}
	built, err := Build(es)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(built.Encode())
	if err != nil {
		t.Fatal(err)
	}
	queries := [][]zhuyin.Syllable{nil, {"ㄅㄚ"}, {"ㄧㄣ", "ㄇㄚˊ"}}
	for _, e := range es {
		for k := 1; k <= len(e.Reading); k++ {
			queries = append(queries, e.Reading[:k])
		}
		queries = append(queries, append(append([]zhuyin.Syllable(nil), e.Reading...), "ㄉㄨㄥˋ"))
	}
	for _, c := range []*Compact{built, decoded} {
		for _, q := range queries {
			if len(q) == 0 {
				continue
			}
			if a, b := norm(c.Exact(q)), norm(mem.Exact(q)); !reflect.DeepEqual(a, b) {
				t.Fatalf("Exact(%v)：%v vs %v", q, a, b)
			}
			if a, b := norm(c.Prefixed(q)), norm(mem.Prefixed(q)); !reflect.DeepEqual(a, b) {
				t.Fatalf("Prefixed(%v)：%v vs %v", q, a, b)
			}
			if c.HasPrefix(q) != mem.HasPrefix(q) {
				t.Fatalf("HasPrefix(%v)", q)
			}
		}
		for _, w := range []string{"丼", "親子", "應該", "景", "不存在", "", "親子丼"} {
			if c.Has(w) != mem.Has(w) {
				t.Fatalf("Has(%q)", w)
			}
		}
	}
}

func TestDecodeRejectsCorruption(t *testing.T) {
	c, err := Build(toyEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	b := c.Encode()
	for _, bad := range [][]byte{nil, b[:len(b)/2], append([]byte("NOTZUIN!"), b[8:]...)} {
		if _, err := Decode(bad); err == nil {
			t.Error("損毀的資料應被拒絕")
		}
	}
	flipped := append([]byte(nil), b...)
	flipped[len(flipped)/2] ^= 0xff
	if _, err := Decode(flipped); err == nil {
		t.Error("CRC 應抓到位元翻轉")
	}
}

func TestBuildKeepsBestDuplicate(t *testing.T) {
	c, err := Build([]Entry{
		{Surface: "丼", Reading: []zhuyin.Syllable{"ㄐㄧㄥˇ"}, PRW: 0.1},
		{Surface: "丼", Reading: []zhuyin.Syllable{"ㄐㄧㄥˇ"}, PRW: 0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if es := c.Exact([]zhuyin.Syllable{"ㄐㄧㄥˇ"}); len(es) != 1 || es[0].PRW != 0.5 {
		t.Errorf("%+v", es)
	}
}

// 同一個詞面的多個讀音，Lookup 的順序必須由資料決定（依讀音排序），不能由排序實作決定：
// 曾經只依詞面做不穩定排序，Go 1.26 與 1.27 得到不同的順序，lexicon.bin 與標音結果都跟著變。
func TestLookupOrderIsTotal(t *testing.T) {
	readings := []string{"ㄆㄧˊ", "ㄅㄟ", "ㄆㄧˋ", "ㄅㄧˋ"}
	var entries []Entry
	for i := range 2000 { // 夠多，不穩定排序才會真的打亂同鍵的元素
		for _, r := range readings {
			entries = append(entries, Entry{Surface: string(rune(0x4E00 + i)), Reading: zhuyin.ParseReading(r), PRW: 0.1})
		}
	}
	c, err := Build(entries)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2000 {
		var got []string
		for _, e := range c.Lookup(string(rune(0x4E00 + i))) {
			got = append(got, zhuyin.Key(e.Reading))
		}
		if len(got) != len(readings) || !sort.StringsAreSorted(got) {
			t.Fatalf("%c 的讀音順序 %v 不是依讀音排序", rune(0x4E00+i), got)
		}
	}
}
