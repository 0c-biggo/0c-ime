// Package importer 把外部來源轉成 data/ 的來源格式。
// 匯入結果先寫到匯入暫存（0c-import -out），和 data/ 的單一詞庫比對後，-apply 的來源才寫回（merge.go）。
package importer

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
	"github.com/0c-biggo/0c-ime/internal/compile"
)

// CharLine 與 WordLine 對應 data/lexicon 的欄位。
type CharLine struct {
	Char, Reading, Grade, Norm, VariantOf, Origin string
}

type WordLine struct {
	Word, Reading, Grade, Norm, Origin string
}

func (c CharLine) String() string {
	return strings.Join([]string{c.Char, c.Reading, c.Grade, c.Norm, c.VariantOf, c.Origin}, "\t")
}

func (w WordLine) String() string {
	return strings.Join([]string{w.Word, w.Reading, w.Grade, w.Norm, w.Origin}, "\t")
}

// IsHan 回報字串是否全由漢字組成。相容表意字（U+F900–FAFF、U+2F800–2FA1F）不算：
// 它們在 Unicode 正規化後等同於一般的字，收進來只會在候選中出現外觀相同的重複字。
func IsHan(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !zhuyin.IsHan(r) || r >= 0xF900 && r <= 0xFAFF || r >= 0x2F800 && r <= 0x2FA1F {
			return false
		}
	}
	return true
}

// RareOrigin 是罕用字的來源標記：不在 Big5 字集的小麥注音單字。編譯器可對這類字降級，
// 讓它們排在常用字之後（仍可輸入，人名用字需要它們）。
const RareOrigin = "mcbopomofo-rare"

func lines(r io.Reader, fn func(string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if err := fn(strings.TrimRight(sc.Text(), "\r")); err != nil {
			return err
		}
	}
	return sc.Err()
}

// ---------------------------------------------------------------- 小麥注音

// McBopomofo 是小麥注音 Source/Data 的內容。
type McBopomofo struct {
	Chars []CharLine
	Words []WordLine
	Occ   map[string]float64 // phrase.occ 的原始次數（子字串計數）
}

// ReadMcBopomofo 讀取 BPMFBase.txt、BPMFMappings.txt、phrase.occ 與破音字優先表。
// 單字等級：破音字第一優先的讀音為 primary、第二與第三優先為 common、其餘為 attested；
// 只有一個讀音的字為 primary。詞的讀音一律為 primary（不同讀音多為變調或又音）。
func ReadMcBopomofo(base, mappings, occ io.Reader, hetero [3]io.Reader) (*McBopomofo, error) {
	m := &McBopomofo{Occ: map[string]float64{}}
	rank := map[string]int{} // "字\t讀音" → 優先順序 1..3
	for i, h := range hetero {
		if h == nil {
			continue
		}
		if err := lines(h, func(l string) error {
			f := strings.Fields(l)
			if len(f) == 2 {
				if _, ok := rank[f[0]+"\t"+f[1]]; !ok {
					rank[f[0]+"\t"+f[1]] = i + 1
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	readings := map[string][]string{}
	rare := map[string]bool{} // 不在 Big5 字集的字
	var order []string
	if err := lines(base, func(l string) error {
		f := strings.Fields(l)
		if len(f) < 2 || !IsHan(f[0]) {
			return nil
		}
		if len(f) >= 5 && !strings.HasPrefix(f[4], "big5") {
			rare[f[0]] = true
		}
		if _, ok := readings[f[0]]; !ok {
			order = append(order, f[0])
		}
		for _, r := range readings[f[0]] {
			if r == f[1] {
				return nil
			}
		}
		readings[f[0]] = append(readings[f[0]], f[1])
		return nil
	}); err != nil {
		return nil, err
	}
	for _, ch := range order {
		rs := readings[ch]
		for _, r := range rs {
			g := "attested"
			switch {
			case len(rs) == 1:
				g = "primary"
			case rank[ch+"\t"+r] == 1:
				g = "primary"
			case rank[ch+"\t"+r] > 1:
				g = "common"
			}
			origin := "mcbopomofo"
			if rare[ch] {
				origin = RareOrigin
			}
			m.Chars = append(m.Chars, CharLine{ch, r, g, "y", "", origin})
		}
	}
	if err := lines(mappings, func(l string) error {
		f := strings.Fields(l)
		if len(f) < 2 || !IsHan(f[0]) {
			return nil
		}
		m.Words = append(m.Words, WordLine{f[0], strings.Join(f[1:], " "), "primary", "y", "mcbopomofo"})
		return nil
	}); err != nil {
		return nil, err
	}
	if err := lines(occ, func(l string) error {
		f := strings.Fields(l)
		if len(f) != 2 || !IsHan(f[0]) {
			return nil
		}
		n, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			return fmt.Errorf("phrase.occ：%q", l)
		}
		m.Occ[f[0]] = n
		return nil
	}); err != nil {
		return nil, err
	}
	return m, nil
}

// FreeCounts 把子字串計數校正成近似「斷詞後」的次數。
//
// phrase.occ 的次數是子字串計數：「台灣大學」出現一次，「台灣」與「台」也各算一次，
// 使短詞被長詞灌水。由長到短處理：一個詞的自由次數 = 原始次數 − 所有包含它的較長詞的
// 自由次數（依出現位置數加權）。這近似於以詞庫做最長匹配斷詞後的計數；重疊與歧義不處理。
// 結果可能為負（計數語料與排除規則不一致），以 0 為下限。
func FreeCounts(occ map[string]float64, lexicon map[string]bool) map[string]float64 {
	words := make([]string, 0, len(lexicon))
	for w := range lexicon {
		words = append(words, w)
	}
	sort.Slice(words, func(i, j int) bool {
		li, lj := len([]rune(words[i])), len([]rune(words[j]))
		if li != lj {
			return li > lj
		}
		return words[i] < words[j]
	})
	covered := map[string]float64{}
	free := map[string]float64{}
	for _, w := range words {
		f := occ[w] - covered[w]
		if f < 0 {
			f = 0
		}
		free[w] = f
		if f == 0 {
			continue
		}
		r := []rune(w)
		for i := 0; i < len(r); i++ {
			for j := i + 1; j <= len(r); j++ {
				if j-i == len(r) {
					continue
				}
				if s := string(r[i:j]); lexicon[s] {
					covered[s] += f
				}
			}
		}
	}
	return free
}

// ---------------------------------------------------------------- 全字庫

// PlaceholderReading 是全字庫疑似用來標記「讀音不明」的讀音。
//
// 全字庫有約 3,900 個字只有 ㄇㄡˇ（「某」）這一個讀音，幾乎都是擴充區的生僻字；真正讀
// ㄇㄡˇ 的字只有十幾個（某、牡、畝⋯）。屬性說明文件沒有寫明，這是推論，所以規則保守：
// 只有在「這是該字唯一的讀音」且 vouched 未替它背書時才略過。
const PlaceholderReading = "ㄇㄡˇ"

// ReadCNS 讀取全字庫的注音屬性與 CNS→Unicode 對照表，回傳單字讀音。
// 只有一個讀音的字為 primary，其餘為 attested（全字庫不提供讀音的常用度）。
// vouched 回報另一個來源是否也收了（字, 讀音）；用來保留真正讀 ㄇㄡˇ 的字。
func ReadCNS(phonetic io.Reader, maps []io.Reader, vouched func(ch, reading string) bool) (out []CharLine, placeholders int, err error) {
	uni := map[string]string{}
	for _, m := range maps {
		if err := lines(m, func(l string) error {
			f := strings.Fields(l)
			if len(f) != 2 {
				return nil
			}
			cp, err := strconv.ParseUint(f[1], 16, 32)
			if err != nil {
				return nil
			}
			uni[f[0]] = string(rune(cp))
			return nil
		}); err != nil {
			return nil, 0, err
		}
	}
	readings := map[string][]string{}
	var order []string
	if err := lines(phonetic, func(l string) error {
		f := strings.Split(l, "\t")
		if len(f) != 2 {
			return nil
		}
		ch, ok := uni[strings.TrimSpace(f[0])]
		if !ok || !IsHan(ch) {
			return nil
		}
		r := strings.TrimSpace(f[1])
		if _, ok := readings[ch]; !ok {
			order = append(order, ch)
		}
		for _, x := range readings[ch] {
			if x == r {
				return nil
			}
		}
		readings[ch] = append(readings[ch], r)
		return nil
	}); err != nil {
		return nil, 0, err
	}
	for _, ch := range order {
		rs := readings[ch]
		if len(rs) == 1 && rs[0] == PlaceholderReading && (vouched == nil || !vouched(ch, rs[0])) {
			placeholders++
			continue
		}
		g := "attested"
		if len(rs) == 1 {
			g = "primary"
		}
		for _, r := range rs {
			out = append(out, CharLine{ch, normalizeCNS(r), g, "y", "", "cns11643"})
		}
	}
	return out, placeholders, nil
}

// normalizeCNS 把全字庫的輕聲寫法（˙ 在音節前）轉成本專案的正規形式（˙ 在音節尾）。
func normalizeCNS(r string) string {
	if strings.HasPrefix(r, "˙") {
		return strings.TrimPrefix(r, "˙") + "˙"
	}
	return r
}

// ---------------------------------------------------------------- CC-CEDICT

// ReadCEDICT 讀取 CC-CEDICT，只取全為漢字的多字詞（2–8 字）。
//
// 讀音：有「Taiwan pr.」時，台灣讀音為 primary、主條目讀音為 common（對岸標準音常是
// 台灣的通行音，docs/16）；沒有時主條目讀音為 primary。known 中已有的詞，主條目讀音一律
// 降為 common——那些詞在台灣的主讀音由 known 的來源決定。
// 略過「variant of」類條目：它們是異體寫法，不是台灣慣用詞形。
func ReadCEDICT(r io.Reader, known map[string]bool) (words []WordLine, skipped int, err error) {
	err = lines(r, func(l string) error {
		if strings.HasPrefix(l, "#") || l == "" {
			return nil
		}
		trad, rest, ok := strings.Cut(l, " ")
		if !ok {
			skipped++
			return nil
		}
		_, rest, _ = strings.Cut(rest, " ") // 簡體
		lb, rb := strings.Index(rest, "["), strings.Index(rest, "]")
		if lb < 0 || rb < lb {
			skipped++
			return nil
		}
		n := len([]rune(trad))
		if n < 2 || n > 8 || !IsHan(trad) {
			return nil
		}
		defs := rest[rb+1:]
		for _, v := range []string{"/variant of", "/old variant of", "/archaic variant of", "/Japanese variant of", "/erroneous variant of"} {
			if strings.HasPrefix(defs, " "+v) {
				return nil
			}
		}
		main, ok := convert(rest[lb+1:rb], n)
		if !ok {
			skipped++
			return nil
		}
		var tw string
		if i := strings.Index(defs, "Taiwan pr. ["); i >= 0 {
			s := defs[i+len("Taiwan pr. ["):]
			if j := strings.Index(s, "]"); j >= 0 {
				tw, _ = convert(s[:j], n)
			}
		}
		g := "primary"
		if tw != "" || known[trad] {
			g = "common"
		}
		words = append(words, WordLine{trad, main, g, "n", "cc-cedict"})
		if tw != "" && tw != main {
			words = append(words, WordLine{trad, tw, "primary", "y", "cc-cedict"})
		}
		return nil
	})
	return
}

func convert(pinyin string, n int) (string, bool) {
	f := strings.Fields(pinyin)
	if len(f) != n {
		return "", false
	}
	out := make([]string, n)
	for i, p := range f {
		s, err := zhuyin.FromPinyin(p)
		if err != nil {
			return "", false
		}
		out[i] = string(s)
	}
	return strings.Join(out, " "), true
}

// ---------------------------------------------------------------- ARPA 語言模型

// ReadARPA 逐行讀取 ARPA 格式的 n-gram 模型，對每一筆呼叫 fn（order、log10 機率、詞序列）。
// 回退權重欄若存在會被忽略：Zuin 以線性內插混合 bigram 與 unigram，不使用回退權重。
func ReadARPA(r io.Reader, fn func(order int, log10p float64, words []string)) error {
	order := 0
	return lines(r, func(l string) error {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || l == "\\data\\" || l == "\\end\\" || strings.HasPrefix(l, "ngram "):
			return nil
		case strings.HasPrefix(l, "\\") && strings.HasSuffix(l, "-grams:"):
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(l, "\\"), "-grams:"))
			if err != nil {
				return fmt.Errorf("ARPA 區段標題 %q", l)
			}
			order = n
			return nil
		}
		if order == 0 {
			return nil
		}
		f := strings.Fields(l)
		if len(f) < order+1 {
			return fmt.Errorf("ARPA 第 %d 階的行欄位不足：%q", order, l)
		}
		p, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			return fmt.Errorf("ARPA 機率 %q", f[0])
		}
		fn(order, p, f[1:order+1])
		return nil
	})
}

// ---------------------------------------------------------------- 證交所 ISIN 股票簡稱

var isinCell = regexp.MustCompile(`<td bgcolor=#FAFAD2>([0-9A-Z]+)\x{3000}([^<]+)</td>`)

// ReadTWSEISIN 從證交所 ISIN 公開清單（已轉 UTF-8）取出四碼股票代號的簡稱。
// 「-KY」「-DR」等後綴與註記符號「*」去掉（國巨* → 國巨、世芯-KY → 世芯），只留 2–6 個漢字的名稱。
func ReadTWSEISIN(r io.Reader) ([]string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range isinCell.FindAllStringSubmatch(string(b), -1) {
		code, name := m[1], strings.TrimSpace(m[2])
		if len(code) != 4 || strings.Trim(code, "0123456789") != "" {
			continue
		}
		name = strings.TrimRight(name, "*＊")
		for _, suf := range []string{"-KY", "-DR"} {
			name = strings.TrimSuffix(name, suf)
		}
		if n := len([]rune(name)); n < 2 || n > 6 || !IsHan(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// ---------------------------------------------------------------- CC-CEDICT 讀音台灣化

// TaiwanReadings 替只有 CC-CEDICT 的詞補上台灣讀音（docs/49）。CC-CEDICT 標的是大陸讀音
// （期 ㄑㄧ、質 ㄓˋ、企 ㄑㄧˇ），從它與小麥注音共有的詞逐字對齊，學出「字＋大陸音 → 台灣音」：
// 同一字與讀音至少出現 3 次、八成以上對到同一個不同的讀音才採用。轉換後的讀音標 primary，
// 原讀音照舊保留，兩岸讀音都打得出來。回傳規則數與新增的讀音。
func TaiwanReadings(cedict, mc []WordLine) (int, []WordLine) {
	mcR, ceR := map[string][]string{}, map[string][]string{}
	for _, w := range mc {
		mcR[w.Word] = append(mcR[w.Word], w.Reading)
	}
	for _, w := range cedict {
		ceR[w.Word] = append(ceR[w.Word], w.Reading)
	}
	for _, m := range []map[string][]string{mcR, ceR} {
		for w, rs := range m {
			sort.Strings(rs)
			m[w] = dedupe(rs)
		}
	}
	type key struct{ ch, from string }
	stat := map[key]map[string]int{}
	var shared []string
	for w := range ceR {
		if mcR[w] != nil {
			shared = append(shared, w)
		}
	}
	sort.Strings(shared)
	for _, w := range shared {
		chars := []rune(w)
		for _, r := range ceR[w] {
			a := strings.Split(r, " ")
			best, bestN := "", -1
			for _, b := range mcR[w] {
				n := 0
				for i, s := range strings.Split(b, " ") {
					if i < len(a) && s == a[i] {
						n++
					}
				}
				if n > bestN {
					best, bestN = b, n
				}
			}
			for i, y := range strings.Split(best, " ") {
				if i < len(a) && i < len(chars) {
					k := key{string(chars[i]), a[i]}
					if stat[k] == nil {
						stat[k] = map[string]int{}
					}
					stat[k][y]++
				}
			}
		}
	}
	rule := map[key]string{}
	for k, c := range stat {
		total, top, topN := 0, "", 0
		for y, n := range c {
			total += n
			if n > topN || n == topN && y < top {
				top, topN = y, n
			}
		}
		if top != k.from && topN >= 3 && float64(topN) >= 0.8*float64(total) {
			rule[k] = top
		}
	}
	have := map[string]bool{}
	for _, w := range cedict {
		have[w.Word+"\t"+w.Reading] = true
	}
	var added []WordLine
	for _, w := range cedict {
		if mcR[w.Word] != nil {
			continue
		}
		chars := []rune(w.Word)
		syl := strings.Split(w.Reading, " ")
		for i := range syl {
			if t, ok := rule[key{string(chars[i]), syl[i]}]; ok {
				syl[i] = t
			}
		}
		if conv := strings.Join(syl, " "); !have[w.Word+"\t"+conv] {
			have[w.Word+"\t"+conv] = true
			added = append(added, WordLine{w.Word, conv, "primary", "y", w.Origin})
		}
	}
	return len(rule), added
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// ---------------------------------------------------------------- 名稱標音

// InWords 是每個字在詞庫多字詞裡各讀音出現的次數（每個詞面讀音算一次）。
type InWords map[string]map[string]int

// WordInternalReadings 統計 words 裡每個字在多字詞中的讀音。只來自 skip 這些來源的詞不算：
// 自動標音的名稱（路名、股票簡稱）自己可能標錯，算進去會把錯誤的讀音當成通行讀音。
func WordInternalReadings(words []compile.WordReading, skip ...string) InWords {
	out := InWords{}
	skipped := map[string]bool{}
	for _, o := range skip {
		skipped[o] = true
	}
	for _, w := range words {
		chars := []rune(w.Word)
		own := false
		for _, o := range w.Origins {
			own = own || !skipped[o]
		}
		if len(chars) < 2 || !own {
			continue
		}
		for i, s := range w.Reading {
			ch := string(chars[i])
			if out[ch] == nil {
				out[ch] = map[string]int{}
			}
			out[ch][string(s)]++
		}
	}
	return out
}

// Fix 修正斷詞標音裡落單的字（single[i] 為真）的讀音：落單的字取單字主讀音，但單字主讀音
// 可能是只在單用時才這樣唸的讀音（「和」單用是口語的 ㄏㄢˋ，名稱裡的佳和、中和路都唸 ㄏㄜˊ）。
// 某個讀音在含這個字的多字詞裡不到一成、另一個讀音過半時，改用那個讀音。屬於詞庫詞的位置
// （濟南、德行）照詞庫的讀音，不動。
func (m InWords) Fix(name string, reading []string, single []bool) []string {
	chars := []rune(name)
	out := append([]string(nil), reading...)
	for i, s := range out {
		if i >= len(single) || !single[i] {
			continue
		}
		c := m[string(chars[i])]
		total := 0
		for _, n := range c {
			total += n
		}
		if total < 20 || float64(c[s]) >= 0.1*float64(total) {
			continue
		}
		best, bestN := "", 0
		for y, n := range c {
			if n > bestN || n == bestN && y < best {
				best, bestN = y, n
			}
		}
		if float64(bestN) > 0.5*float64(total) {
			out[i] = best
		}
	}
	return out
}
