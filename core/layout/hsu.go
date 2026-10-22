package layout

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Hsu 是許氏排列（#183）：只用 26 個字母與空白，同一鍵依目前的組音狀態代表不同符號。
//
// 規則照新酷音 libchewing（src/editor/zhuyin_layout/hsu.rs），小麥注音（Mandarin.cpp CreateHsuLayout）、
// 威注音（Tekkon handleHsu）也相同：
//   - 已經有聲母或介音時 a g h k l m n 是韻母 ㄟ ㄜ ㄛ ㄤ ㄥ ㄢ ㄣ，否則是聲母 ㄘ ㄍ ㄏ ㄎ ㄌ ㄇ ㄋ；
//     已經有介音時 e 是 ㄝ，否則是 ㄧ。
//   - j v c 是 ㄓ ㄔ ㄕ，接 ㄧ、ㄩ 時改成 ㄐ ㄑ ㄒ；ㄐ ㄑ ㄒ 後面接 ㄨ 或沒有介音的韻母時改回 ㄓ ㄔ ㄕ。
//   - 注音暫存不是空的時候 d f j s 是聲調 ˊ ˇ ˋ ˙，空白是一聲；暫存是空的時候它們是聲母 ㄉ ㄈ ㄓ ㄙ。
//   - 按聲調時只有聲母：ㄍ ㄏ ㄇ ㄋ ㄎ ㄌ 改成單獨的韻母 ㄜ ㄛ ㄢ ㄣ ㄤ ㄦ（m 加聲調是「安」），ㄐ ㄑ ㄒ 改成 ㄓ ㄔ ㄕ。
//   - g 也能打 ㄐ：ㄍㄧ、ㄍㄩ 在下一鍵改成 ㄐㄧ、ㄐㄩ（新酷音的容錯）。
//
// 與新酷音不同的一處取威注音的做法：介音已經在、才按 c v（e c 打 ㄧ 再補聲母）時直接是 ㄒ ㄑ，
// 不留下不存在的 ㄕㄧ。單打一個鍵本身的歧義（a 是 ㄘ 或 ㄟ、e 是 ㄧ 或 ㄝ、l 是 ㄦ 或 ㄥ）由 Readings 交給解碼依前後文挑；
// 新酷音是把另一個讀音的注音符號附在候選最後面。
var Hsu Layout = hsu{}

type hsu struct{}

func (hsu) Name() string { return NameHsu }

// hsuTones 是注音暫存不是空的時候的聲調鍵。
var hsuTones = map[rune]rune{' ': zhuyin.Tone1, 'd': 'ˊ', 'f': 'ˇ', 'j': 'ˋ', 's': '˙'}

// hsuFixed 是不看狀態的按鍵；a e g h k l m n 另外判斷。
var hsuFixed = map[rune]rune{
	'b': 'ㄅ', 'p': 'ㄆ', 'f': 'ㄈ', 'd': 'ㄉ', 't': 'ㄊ', 'j': 'ㄓ', 'v': 'ㄔ', 'c': 'ㄕ', 'r': 'ㄖ', 'z': 'ㄗ', 's': 'ㄙ',
	'x': 'ㄨ', 'u': 'ㄩ', 'y': 'ㄚ', 'i': 'ㄞ', 'w': 'ㄠ', 'o': 'ㄡ',
}

// hsuDual 是有聲母或介音時改當韻母的鍵：聲母、韻母。
var hsuDual = map[rune][2]rune{
	'a': {'ㄘ', 'ㄟ'}, 'g': {'ㄍ', 'ㄜ'}, 'h': {'ㄏ', 'ㄛ'}, 'k': {'ㄎ', 'ㄤ'}, 'l': {'ㄌ', 'ㄥ'}, 'm': {'ㄇ', 'ㄢ'}, 'n': {'ㄋ', 'ㄣ'},
}

// hsuLone 是只打了聲母就按聲調時改成的韻母（新酷音 hsu.rs 的收尾修正）。
var hsuLone = map[rune]rune{'ㄏ': 'ㄛ', 'ㄍ': 'ㄜ', 'ㄇ': 'ㄢ', 'ㄋ': 'ㄣ', 'ㄎ': 'ㄤ', 'ㄌ': 'ㄦ'}

var (
	toRetroflex = map[rune]rune{'ㄐ': 'ㄓ', 'ㄑ': 'ㄔ', 'ㄒ': 'ㄕ'}
	toPalatal   = map[rune]rune{'ㄓ': 'ㄐ', 'ㄔ': 'ㄑ', 'ㄕ': 'ㄒ'}
)

func (h hsu) Symbol(key rune) (rune, bool) {
	_, sym, ok := h.Press(zhuyin.Composer{}, key)
	return sym, ok
}

func (hsu) Press(c zhuyin.Composer, key rune) (zhuyin.Composer, rune, bool) {
	keys := c.Keys()
	recorded := keys != "" || c.Empty() // 記錄斷掉（狀態不是由按鍵組成）就不再記，退格改用一般規則
	c = c.Clone()
	if tone, ok := hsuTones[key]; ok && (!c.Empty() || key == ' ') {
		if c.Slot(zhuyin.Medial) == 0 && c.Slot(zhuyin.Final) == 0 {
			ini := c.Slot(zhuyin.Initial)
			if r, ok := toRetroflex[ini]; ok {
				c.Replace(zhuyin.Initial, r)
			} else if r, ok := hsuLone[ini]; ok {
				c.Remove(zhuyin.Initial)
				c.Add(r)
			}
		}
		fixGi(&c) // 收尾修正改了符號時記錄跟著清掉（Replace、Remove），沒改時照舊
		return c, tone, true
	}
	var sym rune
	if d, ok := hsuDual[key]; ok {
		sym = d[0]
		if c.Slot(zhuyin.Initial) != 0 || c.Slot(zhuyin.Medial) != 0 {
			sym = d[1]
		}
	} else if key == 'e' {
		sym = 'ㄧ'
		if c.Slot(zhuyin.Medial) != 0 {
			sym = 'ㄝ'
		}
	} else if r, ok := hsuFixed[key]; ok {
		sym = r
	} else {
		return c, 0, false
	}
	fixGi(&c)
	cl := zhuyin.ClassOf(sym)
	medial := c.Slot(zhuyin.Medial)
	switch {
	case sym == 'ㄨ' || cl == zhuyin.Final && medial == 0:
		if r, ok := toRetroflex[c.Slot(zhuyin.Initial)]; ok {
			c.Replace(zhuyin.Initial, r)
		}
	case sym == 'ㄧ' || sym == 'ㄩ':
		if r, ok := toPalatal[c.Slot(zhuyin.Initial)]; ok {
			c.Replace(zhuyin.Initial, r)
		}
	}
	if r, ok := toPalatal[sym]; ok && (medial == 'ㄧ' || medial == 'ㄩ') {
		sym = r // 威注音：介音先打、再補 c v 時直接是 ㄒ ㄑ
	}
	c.Add(sym)
	if recorded {
		c.SetKeys(keys + string(key))
	}
	return c, sym, true
}

// fixGi：ㄍㄧ、ㄍㄩ 改成 ㄐㄧ、ㄐㄩ（g 也能打 ㄐ），在下一鍵或聲調時才改，同新酷音。
func fixGi(c *zhuyin.Composer) {
	if m := c.Slot(zhuyin.Medial); c.Slot(zhuyin.Initial) == 'ㄍ' && (m == 'ㄧ' || m == 'ㄩ') {
		c.Replace(zhuyin.Initial, 'ㄐ')
	}
}

// replayHsu 從空的組音狀態依序按下 keys（都不是聲調）。
func replayHsu(keys string) zhuyin.Composer {
	var c zhuyin.Composer
	for _, k := range keys {
		c, _, _ = Hsu.Press(c, k)
	}
	return c
}

func (hsu) Backspace(c zhuyin.Composer) zhuyin.Composer {
	if keys := []rune(c.Keys()); len(keys) > 0 {
		return replayHsu(string(keys[:len(keys)-1]))
	}
	c = c.Clone()
	c.Backspace()
	return c
}

// hsuAlias 是單打一個鍵就按聲調時的歧義：預設讀音 → 同一串按鍵的另一個讀音。
// ㄟ、ㄝ、ㄥ 單獨成音節時在許氏只能這樣打（a、e、l 在暫存是空的時候是 ㄘ、ㄧ、ㄌ）。
var hsuAlias = map[zhuyin.Syllable]zhuyin.Syllable{"ㄘ": "ㄟ", "ㄧ": "ㄝ", "ㄦ": "ㄥ"}

func (hsu) Readings(s zhuyin.Syllable) []zhuyin.Syllable {
	if s == "" || !s.Valid() {
		return nil
	}
	base, tone := s.Base(), s.ToneOf()
	for def, alt := range hsuAlias {
		if base == def || base == alt {
			return []zhuyin.Syllable{def.WithTone(tone), alt.WithTone(tone)}
		}
	}
	return nil
}

// hsuKey 是打出各符號的鍵（Keys 用）；ㄐㄑㄒ 與 ㄓㄔㄕ 同鍵，由介音決定。
var hsuKey = map[rune]rune{
	'ㄅ': 'b', 'ㄆ': 'p', 'ㄇ': 'm', 'ㄈ': 'f', 'ㄉ': 'd', 'ㄊ': 't', 'ㄋ': 'n', 'ㄌ': 'l', 'ㄍ': 'g', 'ㄎ': 'k', 'ㄏ': 'h',
	'ㄐ': 'j', 'ㄑ': 'v', 'ㄒ': 'c', 'ㄓ': 'j', 'ㄔ': 'v', 'ㄕ': 'c', 'ㄖ': 'r', 'ㄗ': 'z', 'ㄘ': 'a', 'ㄙ': 's',
	'ㄧ': 'e', 'ㄨ': 'x', 'ㄩ': 'u',
	'ㄚ': 'y', 'ㄛ': 'h', 'ㄜ': 'g', 'ㄝ': 'e', 'ㄞ': 'i', 'ㄟ': 'a', 'ㄠ': 'w', 'ㄡ': 'o', 'ㄢ': 'm', 'ㄣ': 'n', 'ㄤ': 'k', 'ㄥ': 'l', 'ㄦ': 'l',
}

func (hsu) Keys(s zhuyin.Syllable) string {
	var b strings.Builder
	tone := zhuyin.Tone1
	for _, r := range string(s) {
		if zhuyin.ClassOf(r) == zhuyin.Tone {
			tone = r
			continue
		}
		b.WriteRune(hsuKey[r])
	}
	for k, t := range hsuTones {
		if t == tone {
			b.WriteRune(k)
		}
	}
	return b.String()
}

// hsuPunct 是許氏的標點：, . ; 不是注音鍵，直接打「，。；」（同新酷音、小麥注音），' 與 \ 是頓號；
// Shift 組合與標準排列相同。數字、/、- 不是注音鍵也不是標點，沒在組字時交還應用程式。
var hsuPunct = func() map[rune]string {
	m := map[rune]string{',': "，", '.': "。", ';': "；"}
	for k, v := range standardPunct {
		m[k] = v
	}
	return m
}()

func (hsu) Punct(key rune) (string, bool) {
	p, ok := hsuPunct[key]
	return p, ok
}
