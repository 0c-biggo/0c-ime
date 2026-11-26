package decode

import (
	"math"
	"sort"

	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// maxUntonedArcs 是聲調選填（#150）時，每一組（起點、終點）經過沒打聲調位置的詞條上限，依無上下文機率挑選。
// 沒打聲調的單音節展開成五個聲調後常有幾百個字，整句解碼與 N-best 的成本跟弧數成正比；機率排在 64 名以後的
// 字幾乎不會贏，仍可從候選窗選到（候選另外列舉，不受這個上限影響）。和尾端前綴的 TopM 同一個數。
const maxUntonedArcs = defaultTopM

// maxAbbreviatedArcs 是經過簡拼位置的弧上限。只打聲母時同一組（起點、終點）常有上千個詞，
// 全部進入整句解碼與疊字、補全評分會讓每鍵延遲超過手機的預算；候選窗另外列舉，不受這個上限影響。
const maxAbbreviatedArcs = 24

// maxCorrectedArcs 是經過沒打聲調位置、又走了觸控修正讀音（input.Hyp.Corrected：鄰鍵、漏按，#335）的弧上限，依無上下文
// 機率乘 exp(-成本) 挑選。這種弧另外一池，不跟打的音搶 maxUntonedArcs：那個上限只看機率，修正的讀音一多，打的音裡
// 機率較低的字（三鶯的鶯）會被擠掉。打了聲調的位置照舊不裁切（裁切會把鄰鍵讀到的罕用字擠掉，四張犁的犁）。
// 候選窗另外列舉，不受這個上限影響。
const maxCorrectedArcs = 16

// untonedPos 回報音節格第 i 個位置是不是沒打聲調展開的（input.Hyp.Untoned）；全部打了聲調時永遠是 false。
func untonedPos(lat input.Lattice, i int) bool {
	for _, h := range lat[i] {
		if h.Untoned {
			return true
		}
	}
	return false
}

func untonedSpan(lat input.Lattice, start, end int) bool {
	for i := start; i < end; i++ {
		if untonedPos(lat, i) {
			return true
		}
	}
	return false
}

// abbreviatedSpan 回報 [start, end) 是否經過簡拼展開的位置（input.Hyp.Abbreviated）。只看標記、不看列長：
// 桌面的聲調選填加模糊音一列就有 20 個假設，那不是簡拼，弧數上限與裁切方式要跟沒有簡拼時一樣。
func abbreviatedSpan(lat input.Lattice, start, end int) bool {
	for i := start; i < end; i++ {
		for _, h := range lat[i] {
			if h.Abbreviated {
				return true
			}
		}
	}
	return false
}

// lockTouches 回報 [start, end) 是否碰到鎖定；碰到的不裁切，鎖定的詞一定要留著。
func lockTouches(locks map[int]Lock, start, end int) bool {
	for at, l := range locks {
		if at < end && start < at+l.Len {
			return true
		}
	}
	return false
}

// topUntonedArcs 依終點由小到大，各留無上下文機率（含個人化，學過的字不會被裁掉）乘讀音權重最高的 limit 個弧。
// costed 時再乘輸入成本 exp(-cost)：簡拼展開成不是打的那個完整音節要多一點證據，裁切時一併考慮；
// 沒打聲調（#150）的弧照舊不看成本（近音的成本由整句解碼計入），桌面的聲調選填加模糊音結果不變。
func (d *Decoder) topUntonedArcs(pooled map[int][]arc, limit int, costed bool) []arc {
	if len(pooled) == 0 {
		return nil
	}
	ends := make([]int, 0, len(pooled))
	for j := range pooled {
		ends = append(ends, j)
	}
	sort.Ints(ends)
	var out []arc
	for _, j := range ends {
		arcs := pooled[j]
		if len(arcs) <= limit {
			out = append(out, arcs...)
			continue
		}
		score := make([]float64, len(arcs))
		idx := make([]int, len(arcs))
		for k := range arcs {
			score[k] = d.bosProb(arcs[k].e.Surface) * arcs[k].e.PRW
			if costed {
				score[k] *= math.Exp(-arcs[k].cost)
			}
			idx[k] = k
		}
		sort.SliceStable(idx, func(a, b int) bool { return score[idx[a]] > score[idx[b]] })
		for _, k := range idx[:limit] {
			out = append(out, arcs[k])
		}
	}
	return out
}

// bosProb 是 Score.Prob(lm.BOS, w)；逐鍵解碼（BestSearch）時記下來，同一個 searchKey（評分源、版本都相同）之內
// 每個詞只算一次。按鍵圖每一鍵要為上千個詞條排沒打聲調的名次，一半的時間在查這個機率。
func (d *Decoder) bosProb(w string) float64 {
	if d.bos == nil {
		return d.Score.Prob(lm.BOS, w)
	}
	p, ok := d.bos[w]
	if !ok {
		p = d.Score.Prob(lm.BOS, w)
		d.bos[w] = p
	}
	return p
}

// Reading 回傳詞 word 佔音節格 [start, end) 時在詞庫裡的讀音：只看使用者打的音（成本 0 的假設；
// 沒打聲調的音節展開成的五個聲調、簡拼展開的音節也算），以詞庫前綴剪枝逐一組合，取讀音相容權重最高的詞條。
// 找不到時回傳 false。給「聲調選填」（#150）在提交、加詞時把沒打聲調的音節換回詞庫讀音用：
// 學習與自訂詞只收真的讀音，不收內部記號。
func Reading(lex lexicon.Source, lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, bool) {
	r, _, ok := reading(lex, lat, start, end, word, false)
	return r, ok
}

// ReadingCuts 同 Reading，另回傳讀音每個音節結束的位置（見 Seg.Cuts；每個音節佔一個位置時是 nil）。按鍵圖上
// 同一個詞可以有不同切法，取讀音相容權重最高的那一種；同分時取先列舉到的（短的音節在前）。
func ReadingCuts(lex lexicon.Source, lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, []int, bool) {
	return reading(lex, lat, start, end, word, false)
}

// CorrectionReading 同 Reading，但找的是自動修正詞條（Correction）的讀音：聲調選填時推翻自動修正，
// 拒絕次數要記在那個修正詞條自己的讀音上（個人化視圖依它停用修正），不是最後路徑上換上去的詞的讀音（#150）。
func CorrectionReading(lex lexicon.Source, lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, bool) {
	r, _, ok := reading(lex, lat, start, end, word, true)
	return r, ok
}

func reading(lex lexicon.Source, lat input.Lattice, start, end int, word string, correction bool) ([]zhuyin.Syllable, []int, bool) {
	if start < 0 || end > len(lat) || start >= end {
		return nil, nil, false
	}
	var best []zhuyin.Syllable
	var bestCuts []int
	bestPRW := -1.0
	var walk func(pos int, reading []zhuyin.Syllable, cuts []int)
	walk = func(pos int, reading []zhuyin.Syllable, cuts []int) {
		if pos == end {
			for _, e := range lex.Exact(reading) {
				if e.Surface == word && e.Correction == correction && e.PRW > bestPRW {
					best, bestPRW = append([]zhuyin.Syllable(nil), reading...), e.PRW
					bestCuts = nil
					if cuts != nil {
						bestCuts = append([]int(nil), cuts...)
					}
				}
			}
			return
		}
		for _, h := range lat[pos] {
			if h.Cost > 0 && !h.Untoned {
				continue // 近音不是打的音；簡拼展開的成本照樣算打的音
			}
			next := pos + h.Width()
			if next > end {
				continue // 按鍵圖：假設走過了詞的終點
			}
			r := append(reading[:len(reading):len(reading)], h.Syl)
			if lex.HasPrefix(r) {
				c := cuts
				if h.Span > 1 || cuts != nil {
					if cuts == nil {
						c = make([]int, len(reading), len(reading)+1)
						for k := range c {
							c[k] = start + k + 1
						}
					}
					c = append(c[:len(c):len(c)], next)
				}
				walk(next, r, c)
			}
		}
	}
	walk(start, nil, nil)
	return best, bestCuts, best != nil
}
