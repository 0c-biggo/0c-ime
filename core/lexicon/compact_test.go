package lexicon

import (
	"bytes"
	"os"
	"reflect"
	"sort"
	"testing"
	"unsafe"

	"github.com/0c-biggo/0c-ime/core/binfmt"
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
	mapped := mapShifted(t, built.Encode())
	queries := [][]zhuyin.Syllable{nil, {"ㄅㄚ"}, {"ㄧㄣ", "ㄇㄚˊ"}}
	for _, e := range es {
		for k := 1; k <= len(e.Reading); k++ {
			queries = append(queries, e.Reading[:k])
		}
		queries = append(queries, append(append([]zhuyin.Syllable(nil), e.Reading...), "ㄉㄨㄥˋ"))
	}
	for _, c := range []*Compact{built, decoded, mapped} {
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

// mapShifted 把資料包放在錯開一個位元組的位置再 Map：mmap 進來的區段不保證對齊。
// 詞面串接要以 b 本身交給轉換函式（宿主用 unsafe.String 時才真的不複製）。
func mapShifted(t *testing.T, enc []byte) *Compact {
	t.Helper()
	b := append([]byte{0}, enc...)[1:]
	lo, hi := uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&b[len(b)-1]))
	aliased := false
	c, err := Map(b, func(x []byte) string {
		p := uintptr(unsafe.Pointer(&x[0]))
		aliased = lo <= p && p <= hi
		return string(x)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !aliased {
		t.Fatal("詞面串接被複製了")
	}
	if !bytes.Equal(c.Encode(), enc) {
		t.Fatal("Map 後重新編碼的位元組不同")
	}
	return c
}

// 讀音必須依詞條順序連續存放（span 由下一個詞條的起點算長度），不是的要拒絕。
func TestMapRejectsGappedReadings(t *testing.T) {
	c, err := Build(toyEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	w := binfmt.NewWriter(magic, version)
	w.String(sylString(c.syls))
	w.Raw(len(c.rd)/2+1, append(append([]byte(nil), c.rd...), 0, 0)) // 最後多一個沒人用的音節
	for _, col := range []struct {
		n int
		b []byte
	}{{c.n, c.rdOff}, {c.n, c.sfOff}, {c.n, c.sfLen}, {c.n, c.rdLen}, {c.n, c.flags}, {c.n, c.prw}} {
		w.Raw(col.n, col.b)
	}
	w.String(c.strs)
	w.Raw(c.n, c.byText)
	if _, err := Decode(w.Finish()); err == nil {
		t.Fatal("讀音區段有多餘的音節，應被拒絕")
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

// Continuations 必須與逐一問 HasPrefix 相同。
func TestContinuationsMatchHasPrefix(t *testing.T) {
	entries := []Entry{}
	for _, e := range []struct{ w, r string }{
		{"你", "ㄋㄧˇ"}, {"你好", "ㄋㄧˇ ㄏㄠˇ"}, {"你好嗎", "ㄋㄧˇ ㄏㄠˇ ㄇㄚ˙"}, {"你們", "ㄋㄧˇ ㄇㄣ˙"}, {"擬好", "ㄋㄧˇ ㄏㄠˇ"},
		{"好", "ㄏㄠˇ"}, {"好嗎", "ㄏㄠˇ ㄇㄚ˙"}, {"逆", "ㄋㄧˋ"}, {"逆風", "ㄋㄧˋ ㄈㄥ"},
	} {
		entries = append(entries, Entry{Surface: e.w, Reading: zhuyin.ParseReading(e.r), PRW: 1})
	}
	mem := NewMem()
	for _, e := range entries {
		mem.Add(e)
	}
	c, err := Build(entries)
	if err != nil {
		t.Fatal(err)
	}
	all := []zhuyin.Syllable{"ㄋㄧˇ", "ㄋㄧˋ", "ㄏㄠˇ", "ㄇㄚ˙", "ㄇㄣ˙", "ㄈㄥ", "ㄅㄚ"}
	for name, src := range map[string]Source{"compact": c, "mem": mem, "neutral": NeutralParticles(c)} {
		for _, prefix := range [][]zhuyin.Syllable{{"ㄋㄧˇ"}, {"ㄋㄧˇ", "ㄏㄠˇ"}, {"ㄋㄧˋ"}, {"ㄏㄠˇ"}, {"ㄅㄚ"}, {"ㄋㄧˇ", "ㄏㄠˇ", "ㄇㄚ˙"}} {
			for range 2 { // 第二次走快取
				got := map[zhuyin.Syllable]bool{}
				if !Continuations(src, prefix, func(y zhuyin.Syllable) { got[y] = true }) {
					t.Fatalf("%s 應支援 Continuations", name)
				}
				for _, y := range all {
					want := src.HasPrefix(append(prefix[:len(prefix):len(prefix)], y))
					if got[y] != want {
						t.Errorf("%s %v+%s：Continuations %v，HasPrefix %v", name, prefix, y, got[y], want)
					}
				}
			}
		}
	}
	if Continuations(c, nil, func(zhuyin.Syllable) {}) {
		t.Fatal("空前綴不支援")
	}
}
