package zhuyin

import (
	"sort"
	"strings"
)

// 漢語拼音鍵盤（#324）：在鍵盤上打的拼音。跟 FromPinyin（詞庫匯入，接受聲調符號與各種寫法）分開：這裡只有
// 小寫字母，ü 打 v（lv、nve），聲調在外面另外按（session 的拼音鍵盤）。拼法照音節表（pinyinInventory），
// 外加輸入法慣用的寫法 lue、nue（同 lve、nve，新酷音與多數拼音輸入法都收）。

// PinyinSeparator 是隔音符號：把 xi'an 切成兩個音節。
const PinyinSeparator = '\''

// pinyinTyped 是鍵盤拼法 → 不帶聲調的注音音節；pinyinOf 是反過來（每個音節一個標準拼法）。
var pinyinTyped, pinyinOf = func() (map[string]Syllable, map[Syllable]string) {
	typed, of := map[string]Syllable{}, map[Syllable]string{}
	for _, p := range strings.Fields(pinyinInventory) {
		if p == "ê" {
			continue // 單獨的 ㄝ 鍵盤上打不出來（沒有通用寫法），用注音或符號表
		}
		z, err := pinyinBase(p)
		if err != nil {
			panic(err)
		}
		k := strings.ReplaceAll(p, "ü", "v")
		typed[k] = Syllable(z)
		if _, ok := of[Syllable(z)]; !ok {
			of[Syllable(z)] = k
		}
	}
	for _, alias := range []string{"lue", "nue"} {
		typed[alias] = typed[strings.Replace(alias, "u", "v", 1)]
	}
	return typed, of
}()

// pinyinStarts 是所有鍵盤拼法的開頭（含完整拼法）。
var pinyinStarts = func() map[string]bool {
	m := map[string]bool{}
	for k := range pinyinTyped {
		for i := 1; i <= len(k); i++ {
			m[k[:i]] = true
		}
	}
	return m
}()

// MaxPinyinLetters 是一個音節最多的字母數（zhuang、shuang）。
const MaxPinyinLetters = 6

// PinyinSyllable 回傳鍵盤拼法 keys（小寫字母，ü 打 v）代表的不帶聲調音節；不是完整音節時 ok 為 false。
func PinyinSyllable(keys string) (Syllable, bool) {
	y, ok := pinyinTyped[keys]
	return y, ok
}

// PinyinStart 回報 keys 是不是某個音節拼法的開頭（含完整拼法）。
func PinyinStart(keys string) bool { return pinyinStarts[keys] }

// PinyinKeys 回傳不帶聲調的音節 base 在鍵盤上的拼法（ü 打 v）；音節表外的音節回傳空字串。
func PinyinKeys(base Syllable) string { return pinyinOf[base.Base()] }

// PinyinLetter 回報 r 是不是拼音鍵盤的字母（小寫 a–z）。
func PinyinLetter(r rune) bool { return r >= 'a' && r <= 'z' }

// vowelStart：a o e 開頭的音節。拼音正詞法規定這種音節接在別的音節後面時要加隔音符號（xi'an、fang'an），
// 所以沒打 ' 時優先不這樣切。
func vowelStart(k string) bool { return k != "" && strings.ContainsRune("aoe", rune(k[0])) }

// SplitPinyin 把一串鍵盤拼音（小寫字母與隔音符號 '）切成完整的音節，回傳每個音節的拼法（不含 '）；
// 切不成完整音節時 ok 為 false。' 一定是切點。沒有 ' 的一段有好幾種切法時，依序取：
//  1. a o e 開頭、卻接在別的音節後面的音節最少（拼音正詞法：這種地方要打 '，xian 是 xian 不是 xi an）；
//  2. 音節最少；
//  3. 前面的音節最長。
//
// 這是沒有前後文時的切法；session 有詞庫與語言模型時另外在按鍵圖上挑（pinyin.go），這裡是它的退路與顯示。
func SplitPinyin(keys string) (out []string, ok bool) {
	for _, chunk := range strings.Split(keys, string(PinyinSeparator)) {
		if chunk == "" {
			continue
		}
		parts, ok := splitChunk(chunk)
		if !ok {
			return nil, false
		}
		out = append(out, parts...)
	}
	return out, len(out) > 0
}

// PinyinCuts 列出一段沒有 ' 的拼音 chunk 裡所有能當一個完整音節的區段 [i, j)，依起點、長度排序。
func PinyinCuts(chunk string) [][2]int {
	var out [][2]int
	for i := range len(chunk) {
		for j := i + 1; j <= min(len(chunk), i+MaxPinyinLetters); j++ {
			if _, ok := pinyinTyped[chunk[i:j]]; ok {
				out = append(out, [2]int{i, j})
			}
		}
	}
	return out
}

type splitCost struct{ vowels, syls int }

func (a splitCost) less(b splitCost) bool {
	if a.vowels != b.vowels {
		return a.vowels < b.vowels
	}
	return a.syls < b.syls
}

// splitChunk 是 SplitPinyin 對一段沒有 ' 的拼音的切法（從後面往前做動態規劃，同分時前面的音節取長的）。
func splitChunk(chunk string) ([]string, bool) {
	n := len(chunk)
	best := make([]*splitCost, n+1)
	next := make([]int, n+1)
	best[n] = &splitCost{}
	for i := n - 1; i >= 0; i-- {
		for j := min(n, i+MaxPinyinLetters); j > i; j-- { // 長的先試：同分時留前面的音節長的切法
			if _, ok := pinyinTyped[chunk[i:j]]; !ok || best[j] == nil {
				continue
			}
			c := splitCost{vowels: best[j].vowels, syls: best[j].syls + 1}
			if j < n && vowelStart(chunk[j:]) {
				c.vowels++
			}
			if best[i] == nil || c.less(*best[i]) {
				best[i], next[i] = &c, j
			}
		}
	}
	if best[0] == nil {
		return nil, false
	}
	var out []string
	for i := 0; i < n; i = next[i] {
		out = append(out, chunk[i:next[i]])
	}
	return out, true
}

// PinyinInventory 回傳所有鍵盤拼法，依字串排序；供測試與工具使用。
func PinyinInventory() []string {
	out := make([]string, 0, len(pinyinTyped))
	for k := range pinyinTyped {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
