package compile

import (
	"strings"
	"unicode/utf8"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// attestedPRW 估計單字可考讀音的 P(r|w)（#320）。
//
// 單字候選的分數是「字頻 × P(r|w)」。可考讀音固定給 0.1 時，高頻字的罕用音仍會蓋過該音的常用字：
// 「日」的字頻比「密」高兩位數，打 ㄇㄧˋ 首選是「日」（金日磾的讀音）。罕用音的用量不隨字頻成長：
// 「日」唸 ㄇㄧˋ 的次數不會因為「日」常用就變多。小麥注音給破音字優先表以外的讀音一個固定的低分，
// 不跟著字頻（Source/Data/bin_legacy/cook.py 的 H_DEFLT_FREQ）；新酷音的單字詞頻逐讀音記錄，
// 日 ㄇㄧˋ、的 ㄉㄧˋ、大 ㄉㄞˋ 都是 1，與 ㄩˋ（參與）則是 539。
//
// 這裡讓可考讀音的單字分數等於「詞庫裡這個字唸這個音的比例 × 字頻」加上固定的低分 mass，
// 不超過原本的等級值：P(r|w) = min(g, S/T + mass/U)。
//
//   - S：含這個字、而且這個位置唸這個音的多字詞詞頻合計（同一詞有幾個讀音時依等級值分攤）。
//   - T：這個字單獨的詞頻，加上所有含這個字的多字詞詞頻。
//   - U：這個字單獨的詞頻；詞頻都依 -unigram 的權重混合、各來源先正規化。
//   - g：等級值（0.1）。字頻低於 mass/g 的字不受影響；沒有詞頻的字也維持 g。
//
// 高頻字沒有詞例佐證時，罕用音的分數就是 mass，排在常用字之後、仍在候選裡；常用詞裡真的唸這個音
// （參與 ㄘㄢ ㄩˋ、覺得 ㄐㄩㄝˊ ㄉㄜ˙）照比例拿回分數。只調整單字的可考讀音：主讀音、通行音與
// 多字詞的讀音不變。同音沒有常用字（不是可考讀音、也不是只來自罕用來源的字）時不壓低：
// 打 ㄓㄟˋ 只有「這」和簡化字「这」，壓低「這」只會讓「这个」蓋過「這個」。讀音全是可考的字
// （哦、俄）也不動。
//
// 回傳 字 → 讀音 → P(r|w)；只列出低於等級值的讀音。
func attestedPRW(d *Data, keep func([]string) bool, rare func(string) bool, freq func(string) float64, mass float64) map[string]map[zhuyin.Syllable]float64 {
	if mass <= 0 {
		return nil
	}
	// 每個讀音的常用字：不是可考讀音、也不是罕用字。main 是有主讀音或通行音的字。
	common := map[zhuyin.Syllable]map[string]bool{}
	main := map[string]bool{}
	for _, c := range d.Chars {
		if c.Grade != Attested && keep(c.Origins) {
			main[c.Char] = true
			if rare(c.Char) {
				continue
			}
			if common[c.Reading] == nil {
				common[c.Reading] = map[string]bool{}
			}
			common[c.Reading][c.Char] = true
		}
	}
	rival := func(c CharReading) bool {
		for x := range common[c.Reading] {
			if x != c.Char {
				return true
			}
		}
		return false
	}
	// 只有字頻夠高、有主讀音或通行音、同音另有常用字的可考讀音需要估計。讀音全是可考的字
	// （哦、俄）由 normalizeReadings 把最高的讀音當主讀音，可考等級對它沒有意義，維持原狀。
	want := map[string]bool{}
	for _, c := range d.Chars {
		if c.Grade == Attested && keep(c.Origins) && main[c.Char] && freq(c.Char)*c.Grade.PRW() > mass && rival(c) {
			want[c.Char] = true
		}
	}
	type wordReadings struct {
		readings [][]zhuyin.Syllable
		weights  []float64
	}
	words := map[string]*wordReadings{}
	var order []string
	for _, w := range d.Words {
		n := utf8.RuneCountInString(w.Word)
		if !keep(w.Origins) || n < 2 || len(w.Reading) != n {
			continue
		}
		x := words[w.Word]
		if x == nil {
			hit := false
			for _, r := range w.Word {
				hit = hit || want[string(r)]
			}
			if !hit {
				continue
			}
			x = &wordReadings{}
			words[w.Word] = x
			order = append(order, w.Word)
		}
		x.readings = append(x.readings, w.Reading)
		x.weights = append(x.weights, w.Grade.PRW())
	}
	total := map[string]float64{}
	used := map[string]map[zhuyin.Syllable]float64{}
	for ch := range want {
		total[ch] = freq(ch)
		used[ch] = map[zhuyin.Syllable]float64{}
	}
	for _, w := range order {
		x := words[w]
		f := freq(w)
		if f == 0 {
			continue
		}
		sum := 0.0
		for _, v := range x.weights {
			sum += v
		}
		for i, ch := range []rune(w) {
			c := string(ch)
			if !want[c] {
				continue
			}
			total[c] += f
			for k, r := range x.readings {
				used[c][r[i]] += f * x.weights[k] / sum
			}
		}
	}
	out := map[string]map[zhuyin.Syllable]float64{}
	for _, c := range d.Chars {
		if c.Grade != Attested || !want[c.Char] || !rival(c) {
			continue
		}
		p := min(c.Grade.PRW(), used[c.Char][c.Reading]/total[c.Char]+mass/freq(c.Char))
		if p < c.Grade.PRW() {
			if out[c.Char] == nil {
				out[c.Char] = map[zhuyin.Syllable]float64{}
			}
			out[c.Char][c.Reading] = p
		}
	}
	return out
}

// unigramFreq 回傳依 -unigram 權重混合、各來源先正規化的詞頻（不平滑），供編譯期估計比例用。
func unigramFreq(weights map[string]float64, counts map[string]map[string]float64) func(string) float64 {
	sums := map[string]float64{}
	for src, w := range weights {
		if w == 0 {
			continue
		}
		for _, n := range counts[src] {
			sums[src] += n
		}
	}
	return func(x string) float64 {
		f := 0.0
		for src, w := range weights {
			if s := sums[src]; s > 0 {
				f += w * counts[src][x] / s
			}
		}
		return f
	}
}

// rareChars 回傳「只來自罕用來源（-rare）」的單字判斷，與詞庫標 Rare 的規則相同（規則推得的讀音不算來源）。
func rareChars(d *Data, spec string, keep func([]string) bool) func(string) bool {
	src := map[string]bool{}
	for _, x := range strings.Split(spec, ",") {
		if x = strings.TrimSpace(x); x != "" {
			src[x] = true
		}
	}
	common := map[string]bool{}
	note := func(w string, origins []string) {
		for _, x := range origins {
			if !src[x] && !strings.HasPrefix(x, "rule:") {
				common[w] = true
			}
		}
	}
	for _, c := range d.Chars {
		if keep(c.Origins) {
			note(c.Char, c.Origins)
		}
	}
	for _, w := range d.Words {
		if keep(w.Origins) && utf8.RuneCountInString(w.Word) == 1 {
			note(w.Word, w.Origins)
		}
	}
	return func(ch string) bool { return len(src) > 0 && !common[ch] }
}
