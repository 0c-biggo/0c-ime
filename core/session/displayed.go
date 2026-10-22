package session

import (
	"github.com/0c-biggo/0c-ime/core/proto"
	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func (s *Session) shiftDisplayed(at, delta int) {
	if len(s.displayed) == 0 {
		return
	}
	next := map[int]string{}
	for pos, text := range s.displayed {
		switch {
		case pos < at:
			next[pos] = text
		case delta > 0 || pos > at:
			next[pos+delta] = text
		}
	}
	s.displayed = next
}

func (s *Session) fallbackDisplay(pos int, syl zhuyin.Syllable) (string, string) {
	if s.literals[pos] {
		return string(syl), proto.StyleNormal
	}
	// 例如詞庫只有「麻煩」的完整讀音：插入英數後不能把已顯示的「煩」變回注音。
	// 保底文字僅供顯示／提交，不冒充可解碼路徑，也不學成新的單字讀音。
	// 近音保底（nearOnly）同理：不能因為後面多了一個只能靠近音成詞的音節，就把前面已顯示的字變回注音。
	if (len(s.literals) > 0 || s.nearReading) && s.displayed[pos] != "" {
		return s.displayed[pos], proto.StyleNormal
	}
	return syl.Text(), proto.StylePending
}
