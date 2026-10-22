package decode

import (
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

// lockTouches 回報 [start, end) 是否碰到鎖定；碰到的不裁切，鎖定的詞一定要留著。
func lockTouches(locks map[int]Lock, start, end int) bool {
	for at, l := range locks {
		if at < end && start < at+l.Len {
			return true
		}
	}
	return false
}

// topUntonedArcs 依終點由小到大，各留無上下文機率（含個人化，學過的字不會被裁掉）乘讀音權重最高的 maxUntonedArcs 個弧。
func (d *Decoder) topUntonedArcs(pooled map[int][]arc) []arc {
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
		if len(arcs) <= maxUntonedArcs {
			out = append(out, arcs...)
			continue
		}
		score := make([]float64, len(arcs))
		idx := make([]int, len(arcs))
		for k := range arcs {
			score[k] = d.Score.Prob(lm.BOS, arcs[k].e.Surface) * arcs[k].e.PRW
			idx[k] = k
		}
		sort.SliceStable(idx, func(a, b int) bool { return score[idx[a]] > score[idx[b]] })
		for _, k := range idx[:maxUntonedArcs] {
			out = append(out, arcs[k])
		}
	}
	return out
}

// Reading 回傳詞 word 佔音節格 [start, end) 時在詞庫裡的讀音：只看成本 0 的假設（使用者打的音；
// 沒打聲調的音節展開成的五個聲調也是成本 0），以詞庫前綴剪枝逐一組合，取讀音相容權重最高的詞條。
// 找不到時回傳 false。給「聲調選填」（#150）在提交、加詞時把沒打聲調的音節換回詞庫讀音用：
// 學習與自訂詞只收真的讀音，不收內部記號。
func Reading(lex lexicon.Source, lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, bool) {
	return reading(lex, lat, start, end, word, false)
}

// CorrectionReading 同 Reading，但找的是自動修正詞條（Correction）的讀音：聲調選填時推翻自動修正，
// 拒絕次數要記在那個修正詞條自己的讀音上（個人化視圖依它停用修正），不是最後路徑上換上去的詞的讀音（#150）。
func CorrectionReading(lex lexicon.Source, lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, bool) {
	return reading(lex, lat, start, end, word, true)
}

func reading(lex lexicon.Source, lat input.Lattice, start, end int, word string, correction bool) ([]zhuyin.Syllable, bool) {
	if start < 0 || end > len(lat) || start >= end {
		return nil, false
	}
	var best []zhuyin.Syllable
	bestPRW := -1.0
	var walk func(pos int, reading []zhuyin.Syllable)
	walk = func(pos int, reading []zhuyin.Syllable) {
		if pos == end {
			for _, e := range lex.Exact(reading) {
				if e.Surface == word && e.Correction == correction && e.PRW > bestPRW {
					best, bestPRW = append([]zhuyin.Syllable(nil), reading...), e.PRW
				}
			}
			return
		}
		for _, h := range lat[pos] {
			if h.Cost > 0 {
				continue
			}
			r := append(reading[:len(reading):len(reading)], h.Syl)
			if lex.HasPrefix(r) {
				walk(pos+1, r)
			}
		}
	}
	walk(start, nil)
	return best, best != nil
}
