// Package zhuyin 定義注音符號、音節與逐符號組音的狀態機。
//
// 音節的正規字串形式為「聲母 介音 韻母 聲調」，一聲不標、輕聲以 ˙ 置於音節尾，
// 與 data/lexicon 的來源格式一致（例：ㄓㄨˋ、ㄇㄜ˙、ㄔ）。
package zhuyin

import "strings"

// Syllable 是一個完整（含聲調）的注音音節，採正規字串形式。
type Syllable string

// Class 是注音符號在音節中的位置類別。
type Class int

const (
	None Class = iota
	Initial
	Medial
	Final
	Tone
)

const (
	initials = "ㄅㄆㄇㄈㄉㄊㄋㄌㄍㄎㄏㄐㄑㄒㄓㄔㄕㄖㄗㄘㄙ"
	medials  = "ㄧㄨㄩ"
	finals   = "ㄚㄛㄜㄝㄞㄟㄠㄡㄢㄣㄤㄥㄦ"
	// Tone1 是一聲的內部記號；正規字串中不出現。
	Tone1 = 'ˉ'
	tones = "ˉˊˇˋ˙"
)

// ClassOf 回傳符號的類別；非注音符號回傳 None。
func ClassOf(r rune) Class {
	switch {
	case strings.ContainsRune(initials, r):
		return Initial
	case strings.ContainsRune(medials, r):
		return Medial
	case strings.ContainsRune(finals, r):
		return Final
	case strings.ContainsRune(tones, r):
		return Tone
	}
	return None
}

// Valid 檢查音節的結構：各類符號至多一個、順序正確、至少一個發音符號、聲調在尾。
// 這是結構檢查，不是「國語實際存在的音節」清單；後者由詞庫編譯器負責（M1）。
func (s Syllable) Valid() bool {
	last, phonetic := None, 0
	for _, r := range string(s) {
		c := ClassOf(r)
		if c == None || c <= last || r == Tone1 {
			return false
		}
		if c != Tone {
			phonetic++
		}
		last = c
	}
	return phonetic > 0
}

// ParseReading 把以空白分隔的讀音字串切成音節。
func ParseReading(s string) []Syllable {
	fields := strings.Fields(s)
	out := make([]Syllable, len(fields))
	for i, f := range fields {
		out[i] = Syllable(f)
	}
	return out
}

// Key 把音節序列串成可當 map 鍵的字串。
func Key(syls []Syllable) string {
	parts := make([]string, len(syls))
	for i, s := range syls {
		parts[i] = string(s)
	}
	return strings.Join(parts, " ")
}

// Composer 逐符號組出一個音節。同類別的符號後者取代前者，符合一般注音輸入習慣。
type Composer struct {
	slot  [Tone]rune // 以 Class 為索引；Initial..Final
	order []Class    // 輸入順序，供 Backspace 使用
}

// Empty 回報目前是否沒有任何符號。
func (c *Composer) Empty() bool { return len(c.order) == 0 }

// String 回傳尚未完成的符號，依正規順序。
func (c *Composer) String() string {
	var b strings.Builder
	for cl := Initial; cl <= Final; cl++ {
		if c.slot[cl] != 0 {
			b.WriteRune(c.slot[cl])
		}
	}
	return b.String()
}

// Add 加入一個符號。若符號是聲調且已有發音符號，回傳完成的音節並清空。
func (c *Composer) Add(r rune) (done Syllable, ok bool) {
	cl := ClassOf(r)
	switch cl {
	case None:
		return "", false
	case Tone:
		if c.Empty() {
			return "", false
		}
		s := c.String()
		if r != Tone1 {
			s += string(r)
		}
		c.Reset()
		return Syllable(s), true
	}
	if c.slot[cl] == 0 {
		c.order = append(c.order, cl)
	}
	c.slot[cl] = r
	return "", false
}

// Backspace 移除最後輸入的符號；沒有符號時回傳 false。
func (c *Composer) Backspace() bool {
	if c.Empty() {
		return false
	}
	last := c.order[len(c.order)-1]
	c.order = c.order[:len(c.order)-1]
	c.slot[last] = 0
	return true
}

// Reset 清空。
func (c *Composer) Reset() { *c = Composer{} }
