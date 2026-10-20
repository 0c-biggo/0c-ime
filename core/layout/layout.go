// Package layout 把實體按鍵對應到注音符號。新增排列只需新增一個實作。
package layout

import "github.com/0c-biggo/0c-ime/core/zhuyin"

// Layout 是一種鍵盤排列。
type Layout interface {
	Name() string
	// Symbol 回傳按鍵對應的注音符號（聲調含一聲記號 zhuyin.Tone1）。
	Symbol(key rune) (rune, bool)
	// Keys 回傳打出某音節所需的按鍵序列；供測試與工具使用。
	Keys(s zhuyin.Syllable) string
	// Punct 回傳按鍵對應的全形標點（按鍵是實際產生的字元，例如 Shift＋, 為 '<'）。
	Punct(key rune) (string, bool)
}

type table struct {
	name  string
	sym   map[rune]rune
	key   map[rune]rune
	punct map[rune]string
}

func withPunct(p map[rune]string, name string, pairs ...string) *table {
	t := newTable(name, pairs...)
	t.punct = p
	return t
}

func newTable(name string, pairs ...string) *table {
	t := &table{name: name, sym: map[rune]rune{}, key: map[rune]rune{}}
	for _, p := range pairs {
		r := []rune(p)
		t.sym[r[0]] = r[1]
		t.key[r[1]] = r[0]
	}
	return t
}

func (t *table) Name() string { return t.name }

func (t *table) Symbol(key rune) (rune, bool) {
	r, ok := t.sym[key]
	return r, ok
}

func (t *table) Punct(key rune) (string, bool) {
	p, ok := t.punct[key]
	return p, ok
}

func (t *table) Keys(s zhuyin.Syllable) string {
	out := make([]rune, 0, 4)
	hasTone := false
	for _, r := range string(s) {
		out = append(out, t.key[r])
		hasTone = hasTone || zhuyin.ClassOf(r) == zhuyin.Tone
	}
	if !hasTone {
		out = append(out, t.key[zhuyin.Tone1])
	}
	return string(out)
}

// standardPunct 是標準排列下的標點：注音用掉了 , . / ; - 與數字，標點改由 Shift 與其餘按鍵產生。
// 頓號 ' 與 \ 都可以打：\ 是 macOS 內建注音的習慣（#24）。& 打全形的＆（#46）；要半形時在符號上按 ↓ 重選，或切英文模式。
// ^ 打刪節號「……」、_ 打破折號「——」（#118，同 RIME 的全形標點），↓ 可換其他寫法或半形 ^ _。
var standardPunct = map[rune]string{
	'<': "，", '>': "。", '?': "？", ':': "：", '"': "；", '!': "！", '~': "～",
	'[': "「", ']': "」", '{': "『", '}': "』", '\'': "、", '\\': "、", '(': "（", ')': "）", '&': "＆",
	'^': "……", '_': "——",
}

// Standard 是標準（大千）排列。
var Standard Layout = withPunct(standardPunct, NameStandard,
	"1ㄅ", "qㄆ", "aㄇ", "zㄈ", "2ㄉ", "wㄊ", "sㄋ", "xㄌ", "eㄍ", "dㄎ", "cㄏ",
	"rㄐ", "fㄑ", "vㄒ", "5ㄓ", "tㄔ", "gㄕ", "bㄖ", "yㄗ", "hㄘ", "nㄙ",
	"uㄧ", "jㄨ", "mㄩ",
	"8ㄚ", "iㄛ", "kㄜ", ",ㄝ", "9ㄞ", "oㄟ", "lㄠ", ".ㄡ", "0ㄢ", "pㄣ", ";ㄤ", "/ㄥ", "-ㄦ",
	" ˉ", "6ˊ", "3ˇ", "4ˋ", "7˙",
)

// etenPunct 是倚天 41 鍵的標點：' 是 ㄘ，頓號只用 \（標準排列本來就可以）；其他同標準排列。
// 倚天的注音鍵（, . / ; ' - =）用掉的是未按 Shift 的字元，Shift 組合（< > ? : "）照舊是標點，
// 與新酷音、小麥注音的倚天排列相同。= 是 ㄦ，所以倚天排列下沒有 = 開 Emoji 選單（見 session）。
var etenPunct = map[rune]string{
	'<': "，", '>': "。", '?': "？", ':': "：", '"': "；", '!': "！", '~': "～",
	'[': "「", ']': "」", '{': "『", '}': "』", '\\': "、", '(': "（", ')': "）", '&': "＆",
	'^': "……", '_': "——",
}

// ETen 是倚天 41 鍵排列（#83）：一鍵一個符號，聲調在 1（輕聲）2 3 4、一聲是空白；5、6 不是注音鍵。
// 對照與新酷音（libchewing et.rs）、小麥注音（Mandarin.cpp CreateETenLayout）相同。
var ETen Layout = withPunct(etenPunct, NameETen,
	"bㄅ", "pㄆ", "mㄇ", "fㄈ", "dㄉ", "tㄊ", "nㄋ", "lㄌ", "vㄍ", "kㄎ", "hㄏ",
	"gㄐ", "7ㄑ", "cㄒ", ",ㄓ", ".ㄔ", "/ㄕ", "jㄖ", ";ㄗ", "'ㄘ", "sㄙ",
	"eㄧ", "xㄨ", "uㄩ",
	"aㄚ", "oㄛ", "rㄜ", "wㄝ", "iㄞ", "qㄟ", "zㄠ", "yㄡ", "8ㄢ", "9ㄣ", "0ㄤ", "-ㄥ", "=ㄦ",
	" ˉ", "2ˊ", "3ˇ", "4ˋ", "1˙",
)

// 設定檔與命令列使用的排列名稱。
const (
	NameStandard = "standard"
	NameETen     = "eten"
)

// ByName 依名稱回傳排列；不認得的名稱回傳 false。
func ByName(name string) (Layout, bool) {
	switch name {
	case NameStandard:
		return Standard, true
	case NameETen:
		return ETen, true
	}
	return nil, false
}
