package compile

import (
	"fmt"
	"math"
	"sort"
)

// fallbackUnigrams 只補有多來源共同支持的主詞頻缺口。這是保守的編譯期
// 估計，不把辭典收錄當頻次，也不讓某個來源的熱門詞單方面主導缺口。
// 已有主要計數及人工 Boost 的詞只參與最後的共同正規化，彼此比例不變。
func fallbackUnigrams(uni map[string]float64, words, covered map[string]bool, boosted map[string]float64, sources []map[string]float64, scale float64) (int, error) {
	if len(sources) < 2 || !(scale > 0 && scale <= 1) {
		return 0, fmt.Errorf("補足詞頻需要至少兩個來源，scale 須在 (0,1]")
	}
	keys := make([]string, 0, len(words))
	for w := range words {
		keys = append(keys, w)
	}
	sort.Strings(keys)
	sums := make([]float64, len(sources))
	for i, src := range sources {
		for _, w := range keys {
			c := src[w]
			if c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
				return 0, fmt.Errorf("補足詞頻的 %q 計數不合法", w)
			}
			sums[i] += c
		}
		if !(sums[i] > 0) || math.IsInf(sums[i], 0) {
			return 0, fmt.Errorf("補足詞頻來源在目前詞庫沒有有效計數")
		}
	}
	updates := map[string]float64{}
	for _, w := range keys {
		if covered[w] || boosted[w] > 0 {
			continue
		}
		p := 1.0
		for i, src := range sources {
			p = math.Min(p, src[w]/sums[i])
		}
		if p*scale > uni[w] {
			updates[w] = p * scale
		}
	}
	if len(updates) == 0 {
		return 0, nil
	}
	total := 0.0
	for _, w := range keys {
		if p, ok := updates[w]; ok {
			uni[w] = p
		}
		total += uni[w]
	}
	for _, w := range keys {
		uni[w] /= total
	}
	return len(updates), nil
}
