package session

import (
	"strings"
	"unicode"
)

// spaceCJKLatin（#273，設定「中英文之間自動加空格」）在同一次送出的文字裡，漢字與 ASCII 英文字母直接相連的地方
// 補一個半形空格：「今天用VS Code寫Python」→「今天用 VS Code 寫 Python」、「API文件」→「API 文件」。
// 只看相鄰的兩個字元，所以已有空白（半形、全形）、全形標點（「Python，接著」）、半形標點（「C++寫」）、
// 數字（「Python3」「3月」）、換行與全形英數字旁都不補；英文字內部、大小寫與原有的空白不動。
// 前一次送出的字看不到（L1，跟前面已在文件裡的字之間不補），句首、句尾也就不會多出空格。
func spaceCJKLatin(text string) string {
	var b strings.Builder
	prev := rune(-1)
	for i, r := range text {
		if prev >= 0 && (isHan(prev) && isASCIILetter(r) || isASCIILetter(prev) && isHan(r)) {
			if b.Len() == 0 {
				b.Grow(len(text) + 8)
				b.WriteString(text[:i])
			}
			b.WriteByte(' ')
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteRune(r)
		}
		prev = r
	}
	if b.Len() == 0 {
		return text
	}
	return b.String()
}

// isHan：漢字（unicode.Han，含擴充區與相容表意文字）；注音符號、假名、全形標點不算。
func isHan(r rune) bool { return r >= 0x2e80 && unicode.Is(unicode.Han, r) }

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
