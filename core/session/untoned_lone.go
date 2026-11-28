package session

import (
	"strings"

	"github.com/0c-biggo/0c-ime/core/lm"
	"github.com/0c-biggo/0c-ime/core/personal"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 單獨一個不打聲調的音節（#360）。
//
// 沒有前文時，解碼與候選都從句首算：P(w | 句首) 是系統的句首配對與詞頻的內插。系統的句首配對其實是語料的
// 「片段開頭」（語料在非漢字處切開），接在阿拉伯數字後面的字（年、月、日、號、億、萬）在那裡特別多：0.2.13 的資料包
// 「號」的句首配對是它詞頻的 3.4 倍，「好」只有 0.43 倍。打了聲調時是同一個聲調的字互相比，差距通常夠大、看不出來；
// 不打聲調時五個聲調最常用的字放在一起比，ㄏㄠ 就成了「號」在「好」前面、ㄧ 是「以」在「一」前面。
//
// 一段組字（標點、英數隔開的一段）只有一個音節、那個音節沒打聲調、又沒有任何前文時，這個字沒有別的線索：
// 句首不看系統的句首配對，只照詞頻（loneScorer）。解碼、候選、補全與神經評分的 N-best 都經 configureDocumentContext
// 設定評分器，所以組字條顯示的字、Enter 送出的字與候選的順序一致。以下情形照舊，由語言模型決定：
//
//   - 這一段有兩個以上的音節（前後文在組字裡）。
//   - 有已確認的文件前文、接在字面數字或字面英文詞後面（configureDocumentContext 前面幾項）。
//   - 游標前是阿拉伯數字（#337）：句首配對偏向的正是這種位置，「3」後面的 ㄏㄠ 照舊是「號」。
//   - 打了聲調的音節（含打了聲調的簡拼 ㄏˋ）：行為與沒有這條規則時完全相同。
//   - 注音九宮格「自動切字」（nine_segment.go）：位置是鍵、不是音節，一個音節的鍵可以重切成兩個字，不算單獨一個音節。
//
// 沒打聲調的簡拼（ㄏ、ㄧ）、九宮格按聲調鍵完成而聲調未指定的音節、漢語拼音沒打聲調的音節都算。

// loneScorer 在句首不看系統語言模型的句首配對（personal.Model.UnigramProb）；其他前詞照原模型。
// 個人在句首的觀察照算：單獨打 ㄏㄠ 選過「號」的人，下次照樣是「號」。
type loneScorer struct{ *personal.Model }

func (m *loneScorer) Prob(left, w string) float64 {
	if left != lm.BOS {
		return m.Model.Prob(left, w)
	}
	return m.Model.UnigramProb(left, w)
}

func (m *loneScorer) SystemProb(left, w string) float64 {
	if left != lm.BOS {
		return m.Model.SystemProb(left, w)
	}
	return m.Model.UnigramSystemProb(w)
}

func (m *loneScorer) PriorMass(left, w string) float64 {
	if left != lm.BOS {
		return m.Model.PriorMass(left, w)
	}
	return m.Model.UnigramPriorMass(left, w)
}

func (m *loneScorer) HasContext(left string) bool {
	return left == lm.BOS || m.Model.HasContext(left)
}

// toneOpen 回報音節 y 的聲調是不是完全沒打：沒打聲調的音節，或沒打聲調的簡拼（ㄏ*；ㄏ*ˋ 打了聲調，不算）。
func toneOpen(y zhuyin.Syllable) bool {
	return y.Untoned() && (!y.Abbreviated() || strings.HasSuffix(string(y), string(zhuyin.AbbrevMark)))
}

// loneUntoned 回報從 start 開始的那一段組字（到下一個字面文字為止）是不是只有一個音節、而且沒打聲調。
// start 不是一段的開頭時回報 false。
func (s *Session) loneUntoned(start int) bool {
	if start < 0 || start >= len(s.syls) || s.literals[start] || len(s.nineSeg) > 0 {
		return false
	}
	if start > 0 && !s.literals[start-1] || start+1 < len(s.syls) && !s.literals[start+1] {
		return false
	}
	return toneOpen(s.syls[start])
}
