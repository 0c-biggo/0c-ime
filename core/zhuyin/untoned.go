package zhuyin

import (
	"slices"
	"strings"
)

// UntonedMark 是「沒打聲調」的內部記號（#150，聲調選填）：接在音節的注音之後，表示這個音節的聲調由解碼決定。
// 它只出現在 session 的音節序列與音節格展開之前；不進詞庫、學習或任何儲存。
// Valid 與 Legal 都拒絕帶記號的音節，所以任何沒處理它的查詢都只會「查不到」，不會被當成一聲。
const UntonedMark = '?'

// Untone 回傳 base（去掉聲調的注音）的「沒打聲調」音節。
func Untone(base Syllable) Syllable { return Syllable(string(base.Base()) + string(UntonedMark)) }

// AbbrevMark 是「簡拼」的內部記號：只打一個聲母或介音（可再加聲調），代表所有以它開頭的音節。
// 例如 ㄋㄏ 是「你好」，ㄅˇ 是 ㄅ 開頭的三聲音節。記號接在注音之後，使用者打的
// 聲調（一聲寫成 Tone1）再接在記號之後。跟 UntonedMark 一樣只出現在 session 的音節序列與音節格展開之前，
// Valid 與 Legal 都拒絕它；Untoned 也回報 true，讓學習、神經評分與弧數裁切把它當成讀音由解碼決定的音節。
const AbbrevMark = '*'

var toneRunes = [...]rune{1: Tone1, 2: 'ˊ', 3: 'ˇ', 4: 'ˋ', 5: '˙'}

// Abbreviable 回報不帶聲調的 prefix 能不能簡拼：單一個聲母或介音（ㄧ ㄨ ㄩ）。
func Abbreviable(prefix Syllable) bool {
	r := []rune(string(prefix))
	if len(r) != 1 {
		return false
	}
	cl := ClassOf(r[0])
	return (cl == Initial || cl == Medial) && LegalPrefix(prefix)
}

// Abbreviate 回傳只打了 prefix 的簡拼音節；tone 是使用者打的聲調（1–5），0 表示沒打。prefix 不能簡拼時回傳 ""。
func Abbreviate(prefix Syllable, tone int) Syllable {
	if !Abbreviable(prefix) || tone < 0 || tone >= len(toneRunes) {
		return ""
	}
	s := string(prefix) + string(AbbrevMark)
	if tone > 0 {
		s += string(toneRunes[tone])
	}
	return Syllable(s)
}

// abbreviation 拆開簡拼音節：打的注音與聲調（0 是沒打）。
func (s Syllable) abbreviation() (prefix Syllable, tone int, ok bool) {
	t := string(s)
	i := strings.IndexRune(t, AbbrevMark)
	if i < 0 {
		return "", 0, false
	}
	prefix = Syllable(t[:i])
	switch rest := []rune(t[i+len(string(AbbrevMark)):]); len(rest) {
	case 0:
	case 1:
		tone = slices.Index(toneRunes[:], rest[0])
		if tone < 1 {
			return "", 0, false
		}
	default:
		return "", 0, false
	}
	return prefix, tone, Abbreviable(prefix)
}

// Abbreviated 回報音節是否為簡拼音節。
func (s Syllable) Abbreviated() bool {
	_, _, ok := s.abbreviation()
	return ok
}

// abbreviatedBases 是每個可簡拼的注音開頭的所有音節（不帶聲調）；打的注音本身是完整音節時排第一個。
var abbreviatedBases = func() map[Syllable][]Syllable {
	m := map[Syllable][]Syllable{}
	for b := range legalBases {
		p := Syllable(string([]rune(b)[:1]))
		if Abbreviable(p) {
			m[p] = append(m[p], Syllable(b))
		}
	}
	for p, bases := range m {
		slices.SortFunc(bases, func(a, b Syllable) int {
			if (a == p) != (b == p) {
				if a == p {
					return -1
				}
				return 1
			}
			return strings.Compare(string(a), string(b))
		})
	}
	return m
}()

// Untoned 回報音節的讀音是否由解碼決定：沒打聲調的音節（注音結構合法、結尾是 UntonedMark），或簡拼音節。
func (s Syllable) Untoned() bool {
	if s.Abbreviated() {
		return true
	}
	t := string(s)
	if !strings.HasSuffix(t, string(UntonedMark)) {
		return false
	}
	base := Syllable(strings.TrimSuffix(t, string(UntonedMark)))
	return base != "" && base.Valid() && base.ToneOf() == 1 && !strings.ContainsRune(string(base), Tone1)
}

// Tones 回傳讀音由解碼決定的音節可能的讀音：沒打聲調的音節是五個聲調（一聲、二聲、三聲、四聲、輕聲，依序）；
// 簡拼音節是所有以它開頭的音節，打了聲調時只有那個聲調。其他音節原樣回傳只含自己的切片。
func (s Syllable) Tones() []Syllable {
	if p, tone, ok := s.abbreviation(); ok {
		var out []Syllable
		for _, b := range abbreviatedBases[p] {
			if tone > 0 {
				out = append(out, b.WithTone(tone))
				continue
			}
			out = append(out, b, b.WithTone(2), b.WithTone(3), b.WithTone(4), b.WithTone(5))
		}
		return out
	}
	if !s.Untoned() {
		return []Syllable{s}
	}
	b := s.Base()
	return []Syllable{b, b.WithTone(2), b.WithTone(3), b.WithTone(4), b.WithTone(5)}
}

// Text 是音節在畫面上的樣子：沒打聲調的音節不顯示記號；簡拼音節顯示打的注音與聲調（一聲不標）。
func (s Syllable) Text() string {
	if p, tone, ok := s.abbreviation(); ok {
		if tone > 1 {
			return string(p) + string(toneRunes[tone])
		}
		return string(p) // 一聲同完整音節，不標
	}
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
