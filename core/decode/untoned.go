package decode

import (
	"github.com/0c-biggo/0c-ime/core/input"
	"github.com/0c-biggo/0c-ime/core/lexicon"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// Reading 回傳詞 word 佔音節格 [start, end) 時在詞庫裡的讀音：只看成本 0 的假設（使用者打的音；
// 沒打聲調的音節展開成的五個聲調也是成本 0），以詞庫前綴剪枝逐一組合，取讀音相容權重最高的詞條。
// 找不到時回傳 false。給「聲調選填」（#150）在提交、加詞時把沒打聲調的音節換回詞庫讀音用：
// 學習與自訂詞只收真的讀音，不收內部記號。
func Reading(lex lexicon.Source, lat input.Lattice, start, end int, word string) ([]zhuyin.Syllable, bool) {
	if start < 0 || end > len(lat) || start >= end {
		return nil, false
	}
	var best []zhuyin.Syllable
	bestPRW := -1.0
	var walk func(pos int, reading []zhuyin.Syllable)
	walk = func(pos int, reading []zhuyin.Syllable) {
		if pos == end {
			for _, e := range lex.Exact(reading) {
				if e.Surface == word && !e.Correction && e.PRW > bestPRW {
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
