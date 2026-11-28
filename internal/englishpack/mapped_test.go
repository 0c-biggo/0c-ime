package englishpack

import (
	"bytes"
	"encoding/binary"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/0c-biggo/0c-ime/core/english"
)

// packWords 讀出格式 2 words.bin 的所有詞（小寫鍵）。
func packWords(t *testing.T, data []byte) []string {
	t.Helper()
	n := int(binary.LittleEndian.Uint32(data[6:]))
	at := 10
	words := make([]string, 0, n)
	for range n {
		l := int(data[at])
		words = append(words, string(data[at+1:at+1+l]))
		flag := data[at+1+l+8]
		at += 1 + l + 9
		if flag&4 != 0 { // 常見寫法
			at += l
		}
	}
	if at != len(data) {
		t.Fatal("words.bin")
	}
	return words
}

// packPairs 讀出格式 1 bigram.bin 的每種詞對：前詞與它的後詞。
func packPairs(t *testing.T, data []byte) [3][][2]string {
	t.Helper()
	var out [3][][2]string
	at := 6
	word := func() string {
		l := int(data[at])
		w := string(data[at+1 : at+1+l])
		at += 1 + l
		return w
	}
	for kind := range 3 {
		lefts := int(binary.LittleEndian.Uint32(data[at:]))
		at += 4
		for range lefts {
			left := word()
			n := int(binary.LittleEndian.Uint16(data[at:]))
			at += 2
			for range n {
				out[kind] = append(out[kind], [2]string{left, word()})
				at += 4
			}
		}
	}
	if at != len(data) {
		t.Fatal("bigram.bin")
	}
	return out
}

func t9Of(w string) string {
	keys := [10]string{1: "'", 2: "abc", 3: "def", 4: "ghi", 5: "jkl", 6: "mno", 7: "pqrs", 8: "tuv", 9: "wxyz"}
	var out []byte
	for i := range len(w) {
		for k, letters := range keys {
			if letters != "" && strings.IndexByte(letters, w[i]) >= 0 {
				out = append(out, byte('0'+k))
			}
		}
	}
	return string(out)
}

// 實際的詞頻資料包：格式 3（Map）對所有詞的查詢結果跟格式 2（Decode）逐項相同，混打字典也是；
// 詞對的格式 2 對所有前詞與詞對也相同。兩種格式互轉逐位元組相同。
//
// 修正（Corrections、NearCommon）每次約 0.3 ms，預設每 32 個詞查一個；ZERO_C_ENGLISH_ALL=1 查所有詞
// 的三種打錯（32 核心約 1 分鐘）：
//
//	ZERO_C_ENGLISH_ALL=1 go test ./internal/englishpack -run TestMappedPackAnswersLikeDecoded -count=1 -timeout 30m -v
func TestMappedPackAnswersLikeDecoded(t *testing.T) {
	every := 32
	if os.Getenv("ZERO_C_ENGLISH_ALL") != "" {
		every = 1
	}
	d, b, dir := compileRanked(t)
	wordsBin, err := os.ReadFile(filepath.Join(dir, "words.bin"))
	if err != nil {
		t.Fatal(err)
	}
	bigramBin, err := os.ReadFile(filepath.Join(dir, "bigram.bin"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.EncodeMapped()
	if err != nil {
		t.Fatal(err)
	}
	m, err := english.Map(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(m.Encode(), wordsBin) {
		t.Fatal("格式 3 寫回格式 2 不同")
	}
	words := packWords(t, wordsBin)
	if len(words) != d.Len() || m.Len() != d.Len() {
		t.Fatalf("%d words", len(words))
	}
	t.Logf("格式 3：%d 位元組（格式 2 是 %d）", len(data), len(wordsBin))
	for _, pair := range [][2]*english.Dictionary{{d, m}, {d.Mixed(), m.Mixed()}} {
		x, y := pair[0], pair[1]
		if (x == d) != (y == m) {
			t.Fatal("mixed view")
		}
		var mu sync.Mutex
		differ := 0
		check := func(what, typed string, a, b any) {
			if !reflect.DeepEqual(a, b) {
				mu.Lock()
				if differ++; differ < 20 {
					t.Errorf("%s(%q)：%v ≠ %v", what, typed, a, b)
				}
				mu.Unlock()
			}
		}
		prefixes := map[string]bool{}
		t9 := map[string]bool{}
		for _, w := range words {
			for i := 1; i <= len(w); i++ {
				prefixes[w[:i]] = true
			}
			t9[t9Of(w)] = true
		}
		var corrected atomic.Int64
		// 字典唯讀、可以同時查：分給每個 CPU 一份。
		parallel := func(n int, fn func(i int)) {
			var wg sync.WaitGroup
			workers := runtime.GOMAXPROCS(0)
			for k := range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := k; i < n; i += workers {
						fn(i)
					}
				}()
			}
			wg.Wait()
		}
		parallel(len(words), func(n int) {
			w := words[n]
			bare := strings.ReplaceAll(w, "'", "")
			for _, typed := range []string{w, strings.ToUpper(w), strings.ToUpper(w[:1]) + w[1:], bare} {
				check("Has", typed, x.Has(typed), y.Has(typed))
				check("Prior", typed, x.Prior(typed), y.Prior(typed))
				check("Display", typed, x.Display(typed), y.Display(typed))
				check("Suggestible", typed, x.Suggestible(typed), y.Suggestible(typed))
				check("Protected", typed, x.Protected(typed), y.Protected(typed))
				check("Contractions", typed, x.Contractions(typed), y.Contractions(typed))
			}
			if n%every != 0 {
				return
			}
			// 打錯：少打最後一個字母、前兩個字母顛倒、多打一個 q。
			typos := []string{w[:len(w)-1], w + "q"}
			if len(w) > 2 {
				typos = append(typos, w[1:2]+w[:1]+w[2:])
			}
			for _, typed := range typos {
				if typed != "" {
					check("Corrections", typed, x.Corrections(typed, 5), y.Corrections(typed, 5))
					check("NearCommon", typed, x.NearCommon(typed, 39), y.NearCommon(typed, 39))
					corrected.Add(1)
				}
			}
		})
		list := slices.Collect(maps.Keys(prefixes))
		parallel(len(list), func(i int) {
			p := list[i]
			check("Prefix", p, x.Prefix(p, 5), y.Prefix(p, 5))
			if len(p) <= 3 {
				check("Prefix", strings.ToUpper(p), x.Prefix(strings.ToUpper(p), 5), y.Prefix(strings.ToUpper(p), 5))
			}
		})
		list = slices.Collect(maps.Keys(t9))
		parallel(len(list), func(i int) { check("T9", list[i], x.T9(list[i], 5), y.T9(list[i], 5)) })
		t.Logf("%d 個詞、%d 個字首、%d 組電話鍵、%d 個打錯", len(words), len(prefixes), len(t9), corrected.Load())
	}

	mappedPairs := b.EncodeMapped()
	mb, err := english.MapBigrams(mappedPairs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mb.Len() != b.Len() || !bytes.Equal(mb.Encode(), bigramBin) {
		t.Fatal("詞對格式 2 寫回格式 1 不同")
	}
	t.Logf("詞對格式 2：%d 位元組（格式 1 是 %d）", len(mappedPairs), len(bigramBin))
	pairs := packPairs(t, bigramBin)
	for kind, list := range pairs {
		k := english.BigramKind(kind)
		lefts := map[string]bool{}
		for _, p := range list {
			lefts[p[0]] = true
			if x, y := b.Count(k, p[0], p[1]), mb.Count(k, p[0], p[1]); x != y || x == 0 {
				t.Fatalf("Count(%d, %s, %s)：%d ≠ %d", kind, p[0], p[1], x, y)
			}
		}
		for left := range lefts {
			for _, limit := range []int{1, 5, 1000} {
				if x, y := b.After(k, left, limit), mb.After(k, left, limit); !reflect.DeepEqual(x, y) {
					t.Fatalf("After(%d, %s, %d)", kind, left, limit)
				}
			}
		}
	}
}
