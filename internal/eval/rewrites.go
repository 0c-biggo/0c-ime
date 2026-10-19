package eval

// RewriteStats 的分母是每次新音節後可對齊比較的既有字元位置數。
// 只有參考答案與目前組字皆一音節一字時才分類，原樣注音不冒充文字答案。
type RewriteStats struct {
	Compared       int `json:"compared_positions"`
	CorrectToWrong int `json:"correct_to_wrong"`
	WrongToCorrect int `json:"wrong_to_correct"`
	WrongToWrong   int `json:"wrong_to_wrong"`
}

func (s *RewriteStats) Observe(previous, current, want string, syllables int) {
	a, b, w := []rune(previous), []rune(current), []rune(want)
	if len(b) != syllables || len(a) != syllables-1 || len(w) < len(b) {
		return
	}
	for i, x := range a {
		s.Compared++
		if x == b[i] {
			continue
		}
		switch {
		case x == w[i]:
			s.CorrectToWrong++
		case b[i] == w[i]:
			s.WrongToCorrect++
		default:
			s.WrongToWrong++
		}
	}
}
