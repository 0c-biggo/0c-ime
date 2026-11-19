package session

import (
	"maps"
	"slices"
	"strings"

	"github.com/0c-biggo/0c-ime/core/decode"
)

// 觸控外殼按 Enter 或點候選列的整句時，送出畫面上那一句。那句可能是外殼另一個 session
// 融合神經評分的結果；這個 session 打字時不排序（不找 N-best），送出時如果再帶評分器重解一次，
// 低階手機上一句要多等半秒到數秒。commitShown 改成：照常補完沒打完的音節，解出來就是那句就送出；
// 不是的話逐字暫時鎖定那句重解一次，只找文字等於它的最佳斷詞，成本是一次一般解碼。
// 暫時的鎖定不留下，照一般送出學習（不算手選）。有手選鎖、候選開著、英文或對不齊時什麼都不送，
// 回 false，外殼照原本的方式送出。

// commitShown 送出 text；送不出 text 時不提交並回 false（沒打完的音節可能已經補完，和送出前一樣）。
func (s *Session) commitShown(text string) (string, bool) {
	if text == "" || len(s.locks) > 0 || s.cand != nil || s.english || s.mixed.raw != "" || len(s.syls) == 0 {
		return "", false
	}
	s.finishUntoned()
	if !s.comp.Empty() || s.ninePending() {
		return "", false // 音節補不完（例如只有聲調）：照原本的送出處理
	}
	if s.Rerank.Scorer == nil {
		s.refresh()
	}
	if s.snapshot().Text() != text && !s.alignTo(text) {
		return "", false
	}
	return s.commit(), true
}

// alignTo 把路徑換成文字等於 text 的最佳斷詞：每個音節暫時鎖成 text 的那個字（鎖定只限制字、不限制斷詞，
// 見 decode.allowed），解完還原鎖定。對不齊（字數不同、詞庫沒有這種斷法）時不動路徑。
func (s *Session) alignTo(text string) bool {
	runes := []rune(text)
	if len(runes) != len(s.syls) {
		return false
	}
	saved := s.locks
	locks := maps.Clone(saved)
	if locks == nil {
		locks = map[int]decode.Lock{}
	}
	for i, r := range runes {
		if !s.literals[i] {
			locks[i] = decode.Lock{Word: string(r), Len: 1}
		}
	}
	s.locks = locks
	path := s.decodeWithLiterals()
	s.locks = saved
	if pathText(path) != text {
		return false
	}
	s.path = path
	s.shownPath = slices.Clone(path)
	s.displayed = map[int]string{}
	for _, g := range path {
		for i, r := range []rune(g.Word) {
			if !s.literals[g.Start+i] {
				s.displayed[g.Start+i] = string(r)
			}
		}
	}
	return true
}

func pathText(path []decode.Seg) string {
	var b strings.Builder
	for _, g := range path {
		b.WriteString(g.Word)
	}
	return b.String()
}
