package lm

import (
	"slices"
	"unicode/utf8"
)

// VocabQuantile 是自訂詞下限取的百分位（#122）。中位數不行：七成以上的系統詞停在量化的最低機率，
// 各字數的中位數都一樣，自訂姓名會輸給「是＋結」這類單字拼湊；第 90 百分位約是常見詞的入門，
// 而真正常用的同音詞（「世界」約在第 99 百分位以上）照樣排前面。
const VocabQuantile = 0.9

// UniQuantileByLen 回傳各字數詞的 unigram 在 VocabQuantile 的值：第 n 項是 n 個字的詞（第 0 項不用）。
// 自訂詞沒有系統詞頻，以此當下限：剛加入就打得到，但不搶走常用的同音詞。只算一次，之後重用。
func (c *Compact) UniQuantileByLen() []float64 {
	c.quantileOnce.Do(func() {
		c.quantile = quantileByLen(func(yield func(string, float64)) {
			for i := range c.uni {
				yield(c.word(i), float64(c.uni[i]))
			}
		})
	})
	return c.quantile
}

// UniQuantileByLen 同 Compact 的版本；Mem 可以再加詞，所以每次重算。
func (m *Mem) UniQuantileByLen() []float64 {
	return quantileByLen(func(yield func(string, float64)) {
		for w, p := range m.uni {
			yield(w, p)
		}
	})
}

func quantileByLen(each func(func(string, float64))) []float64 {
	var byLen [][]float64
	each(func(w string, p float64) {
		n := utf8.RuneCountInString(w)
		for len(byLen) <= n {
			byLen = append(byLen, nil)
		}
		byLen[n] = append(byLen[n], p)
	})
	out := make([]float64, len(byLen))
	for n, ps := range byLen {
		if len(ps) == 0 {
			continue
		}
		slices.Sort(ps)
		out[n] = ps[int(VocabQuantile*float64(len(ps)-1))]
	}
	return out
}
