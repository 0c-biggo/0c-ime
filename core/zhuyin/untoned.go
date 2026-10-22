package zhuyin

import "strings"

// UntonedMark 是「沒打聲調」的內部記號（#150，聲調選填）：接在音節的注音之後，表示這個音節的聲調由解碼決定。
// 它只出現在 session 的音節序列與音節格展開之前；不進詞庫、學習或任何儲存。
// Valid 與 Legal 都拒絕帶記號的音節，所以任何沒處理它的查詢都只會「查不到」，不會被當成一聲。
const UntonedMark = '?'

// Untone 回傳 base（去掉聲調的注音）的「沒打聲調」音節。
func Untone(base Syllable) Syllable { return Syllable(string(base.Base()) + string(UntonedMark)) }

// Untoned 回報音節是否為沒打聲調的音節（注音結構合法、結尾是 UntonedMark）。
func (s Syllable) Untoned() bool {
	t := string(s)
	if !strings.HasSuffix(t, string(UntonedMark)) {
		return false
	}
	base := Syllable(strings.TrimSuffix(t, string(UntonedMark)))
	return base != "" && base.Valid() && base.ToneOf() == 1 && !strings.ContainsRune(string(base), Tone1)
}

// Tones 回傳沒打聲調的音節在詞庫裡可能的五個聲調（一聲、二聲、三聲、四聲、輕聲，依序）；
// 其他音節原樣回傳只含自己的切片。
func (s Syllable) Tones() []Syllable {
	if !s.Untoned() {
		return []Syllable{s}
	}
	b := s.Base()
	return []Syllable{b, b.WithTone(2), b.WithTone(3), b.WithTone(4), b.WithTone(5)}
}

// Text 是音節在畫面上的樣子：沒打聲調的音節不顯示記號。
func (s Syllable) Text() string {
	if s.Untoned() {
		return string(s.Base())
	}
	return string(s)
}

// HasUntoned 回報 syls 是否含沒打聲調的音節。
func HasUntoned(syls []Syllable) bool {
	for _, y := range syls {
		if y.Untoned() {
			return true
		}
	}
	return false
}

// legalPrefixes 是音節表內所有音節依「聲母 介音 韻母」順序的前綴（含完整音節），不帶聲調。
var legalPrefixes = func() map[string]bool {
	m := map[string]bool{}
	for b := range legalBases {
		r := []rune(b)
		for i := 1; i <= len(r); i++ {
			m[string(r[:i])] = true
		}
	}
	return m
}()

// LegalPrefix 回報不帶聲調的 s 是否為音節表內某個音節的開頭（含完整音節本身），
// 例如 ㄒㄧ、ㄒㄧㄢ 是，ㄉㄧㄤ、ㄅㄩ 不是。不打聲調時用它判斷下一個注音要接在目前的音節後面還是開新的音節。
func LegalPrefix(s Syllable) bool { return legalPrefixes[string(s)] }
