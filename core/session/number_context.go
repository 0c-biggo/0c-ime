package session

import (
	"unicode"

	"git.biggo.com/0c/ime/core/lm"
	"git.biggo.com/0c/ime/core/personal"
)

// numberLefts 代表「某個數字」的左語境。字面文字是解碼邊界，數字後面的音節原本從句首重新解碼；
// 資料包沒有阿拉伯數字（「三個」這類數量多半已併成一個詞），句首又沒有數字的訊息，
// 單音節量詞就取最常見的同音字（「20 為」「20 原」「18 各」）。改用這些中文數詞當前詞、等權平均，
// 讓量詞與單位（位、元、個、年、月、點……）由語言模型的 bigram 自然勝出，而不是硬編碼對照表。
// 清單以真實資料包挑選：單一數詞各有怪癖（「十」後面是芬、佳，「三」後面是味、原），平均後較穩；
// 四、六、七、九帶進較多成語搭配（合、葉），不放。
var numberLefts = []string{"一", "兩", "三", "五", "八", "十", "二十", "百", "幾"}

// numberScorer 在數字後面那一段的句首，把 P(w | BOS) 換成 numberLefts 的平均。
// 只影響解碼與候選排序：畫面上的數字不變，學習照舊以實際的前後文記錄（數詞不會被當成左鄰學進去）。
type numberScorer struct{ *personal.Model }

func (m *numberScorer) mix(left, w string, f func(string, string) float64) float64 {
	if left != lm.BOS {
		return f(left, w)
	}
	p := 0.0
	for _, l := range numberLefts {
		p += f(l, w)
	}
	return p / float64(len(numberLefts))
}
func (m *numberScorer) Prob(left, w string) float64       { return m.mix(left, w, m.Model.Prob) }
func (m *numberScorer) SystemProb(left, w string) float64 { return m.mix(left, w, m.Model.SystemProb) }
func (m *numberScorer) Evidence(left, w string) float64   { return m.mix(left, w, m.Model.Evidence) }
func (m *numberScorer) PriorMass(left, w string) float64  { return m.mix(left, w, m.Model.PriorMass) }
func (m *numberScorer) HasContext(left string) bool {
	return left == lm.BOS || m.Model.HasContext(left)
}

// afterNumber：從 start 開始的組字段緊接在字面數字後面（中間至多一個半形空白）。
// 中英混打句首的「20 」、英文後面的「hello 20 」、數字鍵盤或英數模式在組字中插入的數字都算；
// 字母開頭的型號（A4、mp3、PS5）不是數量，不套用；前面是漢字（我有20）照樣套用。
func (s *Session) afterNumber(start int) bool {
	at := start - 1
	if at >= 0 && s.literals[at] && s.syls[at] == " " {
		at--
	}
	if at < 0 || !s.literals[at] || !isDigitText(string(s.syls[at])) {
		return false
	}
	for at >= 0 && s.literals[at] && isDigitText(string(s.syls[at])) {
		at--
	}
	if at < 0 || !s.literals[at] {
		return true
	}
	for _, r := range string(s.syls[at]) {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// isDigitText：一個字面位置是半形或全形的阿拉伯數字。
func isDigitText(text string) bool {
	r := []rune(text)
	return len(r) == 1 && (r[0] >= '0' && r[0] <= '9' || r[0] >= '０' && r[0] <= '９')
}
