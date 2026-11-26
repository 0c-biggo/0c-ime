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
func spaceCJKLatin(text string) string { return spaceCJK(text, true, false) }

// spaceCJK 是 spaceCJKLatin 的一般形：latin 時漢字與 ASCII 英文字母之間補，digits 時（設定「中文與數字之間自動加空格」）
// 漢字與半形數字 0–9 之間補：「第2版」→「第 2 版」、「2026年11月」→「2026 年 11 月」。規則同上，只看相鄰兩個字元，
// 所以小數點、百分號、全形數字旁不補（「3.5倍」→「3.5 倍」、「50%的」不動）；英文與數字之間本來就不補。
func spaceCJK(text string, latin, digits bool) string {
	other := func(r rune) bool { return latin && isASCIILetter(r) || digits && r >= '0' && r <= '9' }
	var b strings.Builder
	prev := rune(-1)
	for i, r := range text {
		if prev >= 0 && (isHan(prev) && other(r) || other(prev) && isHan(r)) {
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
