package session

import "strings"

// 字面英文詞後面的中文（APP#33）：字面文字是解碼邊界，英文詞後面那一段原本從句首（BOS）算，「check一下」解成
// 「check以下」、「update一下」解成「update以下」：句首的「以下」比「一下」常見，但英文詞後面接的是句子中間。
// 數字後面的一段已經換成數詞語境（number_context.go）；英文詞後面的一段改以那個英文詞當前詞（Decoder.Left）。
// 中文語言模型多半沒有以英文詞為前詞的 bigram，P(w | 英文詞) 就退回單字頻率（personal.Model.Prob 的內插），
// 也就是「句子中間某處」；模型有這個前詞時（詞庫收的英文詞）照它的 bigram。
//
// 只影響解碼與候選排序；畫面上的英文、學習與前文都不變。宿主打開 EnglishContext 才作用（手機的中英混打，
// 2026-11-26）：桌面的混打照舊從句首算，沒有量過桌面的評測。
//
// 量過也用英文詞頻包的英文 → 中文詞對（english.Bigrams 的 EnglishChinese，只有 481 個英文詞）加上所有英文詞
// 合計的後詞分布：APP 的 48 句中英夾雜句最多多對 1 句，接在英文詞後面打 CV300 的句子改壞的一樣多或更多，
// 手機也要多載入 2.1 MB 的詞對，所以不用。

// englishLeft 是從 start 開始的組字段要當前詞的英文詞：start 前面緊接的字面英文詞（中間至多一個半形空白），
// 宿主打開 EnglishContext 才有，否則是空字串。英文詞是連續的字面 ASCII 字母（含撇號，I'm），前面緊接數字的
// （A4、mp3 這類型號）不算。
func (s *Session) englishLeft(start int) string {
	if !s.EnglishContext {
		return ""
	}
	at := start - 1
	if at >= 0 && s.literals[at] && s.syls[at] == " " {
		at--
	}
	end := at + 1
	letter := false
	for ; at >= 0 && s.literals[at]; at-- {
		t := string(s.syls[at])
		if asciiLetters(t) {
			letter = true
		} else if t != "'" {
			break
		}
	}
	if !letter || at >= 0 && s.literals[at] && isDigitText(string(s.syls[at])) {
		return ""
	}
	var b strings.Builder
	for pos := at + 1; pos < end; pos++ {
		b.WriteString(string(s.syls[pos]))
	}
	return strings.Trim(b.String(), "'")
}
