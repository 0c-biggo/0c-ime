// Package input 定義輸入層交給解碼器的契約：帶成本的音節格。
//
// 鍵盤精確輸入時每個位置只有一個音節、成本 0；容錯通道、部分注音與未來的語音前端
// 會在同一位置放入多個帶成本的假設。解碼器對所有前端一視同仁。
package input

import "github.com/0c-biggo/0c-ime/core/zhuyin"

// Hyp 是某個位置的一個音節假設。Cost 為負對數機率，0 表示確定。
type Hyp struct {
	Syl  zhuyin.Syllable
	Cost float64
	// Untoned：這個假設是沒打聲調的音節（#150）或簡拼音節展開出來的讀音。解碼器據此裁切這些位置的弧數，
	// 它的成本也不算「打的音和讀音不同」（不是近音）；全部打了聲調時沒有這種假設。
	Untoned bool
	// Abbreviated：這個假設是簡拼音節展開出來的讀音（同時也是 Untoned）。解碼器據此對經過這個位置的弧
	// 改用較小的上限（decode.maxAbbreviatedArcs）。要明確標記：沒打聲調加模糊音、鄰鍵或九宮格的一列也可能有幾十個
	// 假設，不能從列長推斷是不是簡拼。
	Abbreviated bool
}

// Lattice 的每個元素是一個位置的所有假設。
type Lattice [][]Hyp

// Frontend 把使用者輸入的音節序列展開成音節格。
type Frontend interface {
	Expand(syls []zhuyin.Syllable) Lattice
}

// AbbreviationCost 是簡拼展開成「不是打的那個完整音節」時的成本：只打 ㄕ 時優先是 ㄕ 本身，
// ㄕㄤ、ㄕㄨㄛ 這些展開要多一點證據才會贏（鑰匙不會變成「腰上」）。打的注音本身不是完整音節（ㄅ、ㄋ）時，
// 所有展開同樣是 0，由語言模型決定。
const AbbreviationCost = 4.0

// AbbreviationRunCost 取代 AbbreviationCost：連續只打一個注音的音節裡有不成音節的聲母時（ㄕㄐ 的 ㄐ），
// 使用者確定正在簡拼；完整打音節的人不會單獨打出這種聲母，他們的輸入維持 AbbreviationCost。
const AbbreviationRunCost = 1.0

// abbreviationRuns 回報每個位置是否在「確定正在簡拼」的連續簡拼音節裡。
func abbreviationRuns(syls []zhuyin.Syllable) []bool {
	out := make([]bool, len(syls))
	for i := 0; i < len(syls); {
		if !syls[i].Abbreviated() {
			i++
			continue
		}
		j, definite := i, false
		for ; j < len(syls) && syls[j].Abbreviated(); j++ {
			definite = definite || !syls[j].Base().Legal()
		}
		for ; i < j; i++ {
			out[i] = definite
		}
	}
	return out
}

// Exact 是精確通道：使用者打什麼就是什麼。沒打聲調的音節（#150，只在使用者開啟「聲調選填」時出現）
// 展開成五個聲調，成本都是 0，由語言模型依前後文決定；簡拼音節展開成所有以它開頭的音節（AbbreviationCost）；
// 其他音節只有自己。
type Exact struct{}

func (Exact) Expand(syls []zhuyin.Syllable) Lattice {
	lat := make(Lattice, len(syls))
	runs := abbreviationRuns(syls)
	for i, s := range syls {
		if s.Untoned() {
			typed := s.Base()
			for _, y := range s.Tones() {
				h := Hyp{Syl: y, Untoned: true, Abbreviated: s.Abbreviated()}
				if s.Abbreviated() && typed.Legal() && y.Base() != typed {
					h.Cost = AbbreviationCost
					if runs[i] {
						h.Cost = AbbreviationRunCost
					}
				}
				lat[i] = append(lat[i], h)
			}
			continue
		}
		lat[i] = []Hyp{{Syl: s}}
	}
	return lat
}
