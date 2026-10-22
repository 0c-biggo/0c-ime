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
	// keys 是依狀態解讀按鍵的排列（許氏，#183）打出目前符號的按鍵：從空的組音狀態依序重播這些按鍵，
	// 會得到現在的符號。退格時去掉最後一鍵重播，才能把那一鍵順便改掉的符號（ㄓ→ㄐ）一起還原。
	// 空字串表示沒有記錄（一鍵一符號的排列，或符號不是由按鍵組成）；任何直接修改符號的操作都清掉它。
	keys string
}

// Keys 回傳打出目前符號的按鍵（見 keys 欄位）；沒有記錄時為空字串。
func (c *Composer) Keys() string { return c.keys }

// SetKeys 記下打出目前符號的按鍵；呼叫端保證從空的狀態重播這些按鍵會得到目前的符號。
func (c *Composer) SetKeys(keys string) { c.keys = keys }

// Clone 回傳不與 c 共用輸入順序的複本：之後對任何一方 Add、Backspace 都不影響另一方。
func (c Composer) Clone() Composer {
	c.order = append([]Class(nil), c.order...)
	return c
}

// Replace 把某類別目前的符號換成 r，輸入順序不變；該類別沒有符號時不動作。
func (c *Composer) Replace(cl Class, r rune) {
	if cl < Initial || cl > Final || c.slot[cl] == 0 {
		return
	}
	c.slot[cl] = r
	c.keys = ""
}

// Remove 移除某類別的符號。
func (c *Composer) Remove(cl Class) {
	if cl < Initial || cl > Final || c.slot[cl] == 0 {
		return
	}
	c.slot[cl] = 0
	order := make([]Class, 0, len(c.order))
	for _, o := range c.order {
		if o != cl {
			order = append(order, o)
		}
	}
	c.order = order
	c.keys = ""
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

// Slot 回傳目前在該類別（聲母、介音、韻母）的符號；沒有時回傳 0。
func (c *Composer) Slot(cl Class) rune {
	if cl < Initial || cl > Final {
		return 0
	}
	return c.slot[cl]
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
	c.keys = ""
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
	c.keys = ""
	return true
}

// Reset 清空。
func (c *Composer) Reset() { *c = Composer{} }
