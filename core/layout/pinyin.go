package layout

import (
	"strconv"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Pinyin 是漢語拼音鍵盤（#324）：用拼音打繁體，字母是拼音、數字 1–5 是聲調（5 是輕聲），同新酷音的「漢語拼音」鍵盤配置。
//
// 拼音不是一鍵一個注音符號（zh 是 ㄓ、ju 的 u 是 ㄩ），所以這個排列沒有注音鍵：Symbol、Press 都回 false，
// 字母、聲調與隔音符號 ' 由 session 的拼音鍵盤（core/session/pinyin.go）收成一串拼音，切成音節後轉成注音交給同一個引擎。
// 標點同許氏：, . ; 不是拼音鍵，直接打「，。；」，Shift 組合與標準排列相同；' 在拼音後面是隔音符號，其他時候是頓號。
var Pinyin Layout = pinyin{}

type pinyin struct{}

func (pinyin) Name() string { return NamePinyin }

func (pinyin) Symbol(rune) (rune, bool) { return 0, false }

func (pinyin) Press(c zhuyin.Composer, _ rune) (zhuyin.Composer, rune, bool) { return c, 0, false }

func (pinyin) Backspace(c zhuyin.Composer) zhuyin.Composer {
	c = c.Clone()
	c.Backspace()
	return c
}

func (pinyin) Readings(zhuyin.Syllable) []zhuyin.Syllable { return nil }

// Keys 回傳打出 s 的拼音與聲調數字（ㄋㄧˇ 是 ni3、ㄇㄚ 是 ma1、ㄉㄜ˙ 是 de5）；沒打聲調的音節只有拼音。
// 音節表外的音節回傳空字串。
func (pinyin) Keys(s zhuyin.Syllable) string {
	k := zhuyin.PinyinKeys(s)
	if k == "" || s.Untoned() {
		return k
	}
	return k + strconv.Itoa(s.ToneOf())
}

func (pinyin) Punct(key rune) (string, bool) {
	p, ok := hsuPunct[key]
	return p, ok
}
