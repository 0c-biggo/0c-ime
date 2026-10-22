// Package layout 把實體按鍵對應到注音符號。新增排列只需新增一個實作。
//
// 標準、倚天 41 鍵是一鍵一個符號的靜態表格；許氏（#183）同一鍵依目前的組音狀態代表不同符號，
// 所以組字一律經 Press 解讀，session 不自己查表。
package layout

import "github.com/0c-biggo/0c-ime/core/zhuyin"

// Layout 是一種鍵盤排列。
type Layout interface {
	Name() string
	// Symbol 回傳按鍵在注音暫存是空的時候代表的符號（聲調含一聲記號 zhuyin.Tone1），也就是「這個鍵是不是注音鍵」。
	// 靜態排列的答案與狀態無關；依狀態解讀的排列（許氏）在組字時要用 Press。
	Symbol(key rune) (rune, bool)
	// Press 依組音狀態 c 解讀一個按鍵，回傳按下後的組音狀態與這一鍵打出的符號；不是注音鍵時 ok 為 false。
	// 一般符號已經加進回傳的狀態；聲調（含空白的一聲）不加，回傳的是收尾修正過、還沒加聲調的狀態
	// （許氏只打 ㄇ 就按聲調時是 ㄢ），由呼叫端加上聲調完成音節。c 不會被修改。
	Press(c zhuyin.Composer, key rune) (next zhuyin.Composer, sym rune, ok bool)
	// Backspace 退掉組音狀態的最後一鍵。c 不會被修改。
	Backspace(c zhuyin.Composer) zhuyin.Composer
	// Readings 回傳打出 s 的同一串按鍵可能代表的所有讀音，第一個是按鍵的預設讀音（許氏單打 a 是 ㄘ，也可能是 ㄟ）；
	// 按鍵沒有歧義時回傳 nil。
	Readings(s zhuyin.Syllable) []zhuyin.Syllable
	// Keys 回傳打出某音節所需的按鍵序列；供測試與工具使用。歧義讀音（許氏的 ㄟ）回傳預設讀音的按鍵。
	Keys(s zhuyin.Syllable) string
	// Punct 回傳按鍵對應的全形標點（按鍵是實際產生的字元，例如 Shift＋, 為 '<'）。
	Punct(key rune) (string, bool)
}

// AltCost 是同一串按鍵的非預設讀音（Readings 的第二個起）在音節格中的成本（自然對數尺度）。
// 許氏的歧義是排列本身的，使用者按的鍵兩種讀音都對，不是打錯：成本只讓前後文沒有明顯偏好時取預設讀音，
// 比近音的換音成本（fuzzy.SwapCost 1.2）小，學過的詞（例如選過的「欸」）可以勝過它。
const AltCost = 0.7

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

func (t *table) Press(c zhuyin.Composer, key rune) (zhuyin.Composer, rune, bool) {
	sym, ok := t.sym[key]
	if !ok {
		return c, 0, false
	}
	if zhuyin.ClassOf(sym) == zhuyin.Tone {
		return c, sym, true
	}
	c = c.Clone()
	c.Add(sym)
	return c, sym, true
}

func (t *table) Backspace(c zhuyin.Composer) zhuyin.Composer {
	c = c.Clone()
	c.Backspace()
	return c
}

func (t *table) Readings(zhuyin.Syllable) []zhuyin.Syllable { return nil }

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
	NameHsu      = "hsu"
)

// ByName 依名稱回傳排列；不認得的名稱回傳 false。
func ByName(name string) (Layout, bool) {
	switch name {
	case NameStandard:
		return Standard, true
	case NameETen:
		return ETen, true
	case NameHsu:
		return Hsu, true
	}
	return nil, false
}
