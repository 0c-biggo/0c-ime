// Package zhconv 把台灣正體轉成簡體，比對規則照 OpenCC 的 tw2s 設定（#118）。
//
// 字典是 OpenCC 上游的文字檔原文，由宿主讀進來再交給 TW2S；這裡只有比對邏輯，不碰檔案。
// tw2s 的步驟：
//
//  1. 正規化：CJK 相容表意字換成統一表意字（CJK_Compatibility_Ideographs）。
//  2. 分段：以 TSPhrases 由左而右取最長詞（OpenCC 的 mmseg）；比對不到的字連成一段。
//  3. 每段依序經過兩組字典，各自由左而右取最長的鍵，換成第一個值：
//     先「TWVariantsRevPhrases → TWVariantsRev」（台灣字形回到 OpenCC 標準字形），
//     再「TSPhrases → TSCharacters」（繁轉簡）。一組裡第一本比對得到的字典勝出（short_circuit）。
//
// 上游標成「可能變豆腐字」的延伸字典（TSCharactersExt）不用，同 OpenCC 命令列的預設：
// 這些字的簡體在多數字型裡沒有字形。
package zhconv

import (
	"fmt"
	"strings"
)

// Dict 是一本 OpenCC 字典：鍵 → 第一個值（OpenCC 轉換時一律取第一個值）。
type Dict struct {
	m   map[string]string
	max int // 最長的鍵（字元數）
}

// ParseDict 讀 OpenCC 的文字字典：每行「鍵<Tab>值 值…」，# 開頭與空行略過。
// 重複的鍵以第一次出現的為準。
func ParseDict(text string) (*Dict, error) {
	d := &Dict{m: map[string]string{}}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, values, ok := strings.Cut(line, "\t")
		if !ok || key == "" || values == "" {
			return nil, fmt.Errorf("第 %d 行不是「鍵<Tab>值」：%q", i+1, line)
		}
		first, _, _ := strings.Cut(values, " ")
		d.add(key, first)
	}
	return d, nil
}

// ParseReversed 讀一本字典並把鍵值對調（OpenCC data/scripts/reverse.py，用來產生 TWVariantsRev）：
// 同一個值對到好幾個鍵時取檔案裡最先出現的鍵，「# @reverse-prefer: 值 [鍵]」指定例外
// （只寫一個字時鍵與值相同）。
func ParseReversed(text string) (*Dict, error) {
	const prefer = "# @reverse-prefer:"
	reversed := map[string][]string{}
	var order []string
	preferred := map[string]string{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := strings.CutPrefix(line, prefer); ok {
			f := strings.Fields(rest)
			if len(f) != 1 && len(f) != 2 {
				return nil, fmt.Errorf("第 %d 行的 @reverse-prefer 格式不對：%q", i+1, line)
			}
			preferred[f[0]] = f[len(f)-1]
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, values, ok := strings.Cut(line, "\t")
		if !ok || key == "" || values == "" {
			return nil, fmt.Errorf("第 %d 行不是「鍵<Tab>值」：%q", i+1, line)
		}
		for _, v := range strings.Split(values, " ") {
			if _, seen := reversed[v]; !seen {
				order = append(order, v)
			}
			reversed[v] = append(reversed[v], key)
		}
	}
	d := &Dict{m: map[string]string{}}
	for _, v := range order {
		keys := reversed[v]
		first := keys[0]
		if p, ok := preferred[v]; ok {
			found := false
			for _, k := range keys {
				found = found || k == p
			}
			if !found {
				return nil, fmt.Errorf("@reverse-prefer %s → %s 沒有對應的詞條", v, p)
			}
			first = p
		}
		d.add(v, first)
	}
	for v, p := range preferred {
		if _, ok := reversed[v]; !ok {
			return nil, fmt.Errorf("@reverse-prefer %s → %s 沒有對應的詞條", v, p)
		}
	}
	return d, nil
}

func (d *Dict) add(key, value string) {
	if _, dup := d.m[key]; dup {
		return
	}
	d.m[key] = value
	d.max = max(d.max, len([]rune(key)))
}

// Len 回報詞條數。
func (d *Dict) Len() int { return len(d.m) }

// match 回傳從 r[i] 起最長的鍵的長度與值；沒有時長度為 0。
func (d *Dict) match(r []rune, i int) (int, string) {
	for n := min(d.max, len(r)-i); n > 0; n-- {
		if v, ok := d.m[string(r[i:i+n])]; ok {
			return n, v
		}
	}
	return 0, ""
}

// group 是 OpenCC short_circuit 的字典組：依序查，第一本比對得到的字典勝出（即使後面的鍵更長）。
type group []*Dict

func (g group) match(r []rune, i int) (int, string) {
	for _, d := range g {
		if n, v := d.match(r, i); n > 0 {
			return n, v
		}
	}
	return 0, ""
}

// convert 由左而右取最長的鍵換成值，比對不到的字照原樣。
func (g group) convert(r []rune) []rune {
	out := make([]rune, 0, len(r))
	for i := 0; i < len(r); {
		if n, v := g.match(r, i); n > 0 {
			out = append(out, []rune(v)...)
			i += n
			continue
		}
		out = append(out, r[i])
		i++
	}
	return out
}

// Converter 是一條轉換鏈；建好後唯讀，可同時給多個 session 使用。
type Converter struct {
	normalize group
	segment   *Dict
	chain     []group
}

// Data 是 tw2s 用到的 OpenCC 上游字典原文。
type Data struct {
	CJKCompatibility     string // CJK_Compatibility_Ideographs.txt
	TWVariants           string // TWVariants.txt（反轉成 TWVariantsRev）
	TWVariantsRevPhrases string // TWVariantsRevPhrases.txt
	TSPhrases            string // TSPhrases.txt
	TSCharacters         string // TSCharacters.txt
}

// TW2S 依 OpenCC tw2s.json 建立台灣正體轉簡體的轉換器。
func TW2S(data Data) (*Converter, error) {
	parse := func(name, text string, reverse bool) (*Dict, error) {
		var d *Dict
		var err error
		if reverse {
			d, err = ParseReversed(text)
		} else {
			d, err = ParseDict(text)
		}
		if err == nil && d.Len() == 0 {
			err = fmt.Errorf("沒有詞條")
		}
		if err != nil {
			return nil, fmt.Errorf("%s：%w", name, err)
		}
		return d, nil
	}
	compat, err := parse("CJK_Compatibility_Ideographs", data.CJKCompatibility, false)
	if err != nil {
		return nil, err
	}
	variants, err := parse("TWVariants", data.TWVariants, true)
	if err != nil {
		return nil, err
	}
	variantPhrases, err := parse("TWVariantsRevPhrases", data.TWVariantsRevPhrases, false)
	if err != nil {
		return nil, err
	}
	phrases, err := parse("TSPhrases", data.TSPhrases, false)
	if err != nil {
		return nil, err
	}
	chars, err := parse("TSCharacters", data.TSCharacters, false)
	if err != nil {
		return nil, err
	}
	return &Converter{
		normalize: group{compat},
		segment:   phrases,
		chain:     []group{{variantPhrases, variants}, {phrases, chars}},
	}, nil
}

// Convert 把台灣正體轉成簡體；英文、數字、標點、Emoji 與注音符號不在字典裡，照原樣保留。
func (c *Converter) Convert(s string) string { return c.ConvertPinned(s, nil) }

// ConvertPinned 同 Convert，但 pins 指定的位置（以字元計，對應 s）直接輸出指定的文字，
// 不參與比對：用來依讀音決定一字多義的寫法（見 Pin）。指定的位置同時是分段的邊界，
// 跨過它的詞不會被整詞比對。
func (c *Converter) ConvertPinned(s string, pins map[int]string) string {
	if s == "" {
		return s
	}
	r := c.normalize.convert([]rune(s))
	if len(r) != len([]rune(s)) {
		pins = nil // 正規化表都是一對一；萬一不是，位置對不上就不指定
	}
	var b strings.Builder
	start := 0
	for i := 0; i <= len(r); i++ {
		pin, pinned := pins[i]
		if i < len(r) && !pinned {
			continue
		}
		c.convertSpan(&b, r[start:i])
		if pinned && i < len(r) {
			b.WriteString(pin)
		}
		start = i + 1
	}
	return b.String()
}

// convertSpan 分段後每段經過整條轉換鏈。
func (c *Converter) convertSpan(b *strings.Builder, r []rune) {
	run := 0 // 比對不到詞的字連成一段
	flush := func(seg []rune) {
		for _, g := range c.chain {
			seg = g.convert(seg)
		}
		b.WriteString(string(seg))
	}
	for i := 0; i < len(r); {
		n, _ := c.segment.match(r, i)
		if n == 0 {
			i++
			continue
		}
		if run < i {
			flush(r[run:i])
		}
		flush(r[i : i+n])
		i += n
		run = i
	}
	if run < len(r) {
		flush(r[run:])
	}
}

// pinned 是讀音能決定簡體寫法、而 OpenCC 依詞面常判斷錯的字（#118）：
// 「著」讀 ㄓㄨˋ（土著、原著、編著）時簡體仍是「著」，OpenCC 只看字會轉成「着」；
// 「乾」讀 ㄑㄧㄢˊ（乾安、乾德）留「乾」、讀 ㄍㄢ 才是「干」；「瞭」讀 ㄌㄧㄠˋ（瞭望）留「瞭」、讀 ㄌㄧㄠˇ 是「了」；
// 「沈」讀 ㄔㄣˊ（下沈、消沈、沈穩）是「沉」、讀 ㄕㄣˇ（姓）留「沈」。
// 「著」的其他讀音（ㄓㄠˊ、ㄓㄜ˙、ㄓㄨㄛˊ）不指定，交給 OpenCC 的詞表（「顯著」也收了 ㄒㄧㄢˇ ㄓㄜ˙，
// 不能因此轉成「显着」）。
var pinned = map[rune]map[string]string{
	'著': {"ㄓㄨˋ": "著"},
	'乾': {"ㄑㄧㄢˊ": "乾", "ㄍㄢ": "干"},
	'瞭': {"ㄌㄧㄠˋ": "瞭", "ㄌㄧㄠˇ": "了"},
	'沈': {"ㄔㄣˊ": "沉", "ㄕㄣˇ": "沈"},
}

// Pin 回傳字 c 讀 reading（注音，輕聲 ˙ 在後）時應固定輸出的簡體；讀音決定不了時回傳 false。
func Pin(c rune, reading string) (string, bool) {
	v, ok := pinned[c][reading]
	return v, ok
}
