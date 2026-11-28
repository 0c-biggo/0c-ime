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

// spaceCJK 是 spaceCJKLatin 的一般形：latin 時漢字與 ASCII 英文字母之間補，digits 時（設定「自動加空格」選
// 「中英文與數字之間」）漢字與半形數字 0–9 之間補：「第2版」→「第 2 版」、「2026年11月」→「2026 年 11 月」。
// 規則同上，只看相鄰兩個字元，所以小數點、全形數字旁不補（「3.5倍」→「3.5 倍」）；英文與數字之間本來就不補。
// 例外（#365）：緊跟在半形數字後面的半形 % 算數字的一部分，它跟後面的漢字之間也補（「有50%的人」→「有 50% 的人」）；
// 前面不是數字的 %（「折扣%的」「50%%的」）與全形 ％ 照舊不補。
func spaceCJK(text string, latin, digits bool) string { return spaceCommit(text, latin, digits, nil) }

// spaceCommit 是送出時實際用的（#365）：同 spaceCJK，但 verbatim 列的文字在 text 裡出現的地方照原樣，內部不補。
// verbatim 是這次組字從「我的片語」選進來的片語：使用者自己寫好的內容（地址、統編、Email）不改寫，英文、數字都一樣。
// 片語跟前後文字相接的地方不屬於片語內部，照一般規則補（「用」＋片語「ABC」→「用 ABC」）。
func spaceCommit(text string, latin, digits bool, verbatim []string) string {
	other := func(r rune) bool { return latin && isASCIILetter(r) || digits && isASCIIDigit(r) }
	inside := verbatimInside(text, verbatim)
	var b strings.Builder
	prev, prev2 := rune(-1), rune(-1)
	for i, r := range text {
		percent := digits && prev == '%' && isASCIIDigit(prev2)
		if prev >= 0 && !(inside != nil && inside[i]) && (isHan(prev) && other(r) || (other(prev) || percent) && isHan(r)) {
			if b.Len() == 0 {
				b.Grow(len(text) + 8)
				b.WriteString(text[:i])
			}
			b.WriteByte(' ')
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteRune(r)
		}
		prev, prev2 = r, prev
	}
	if b.Len() == 0 {
		return text
	}
	return b.String()
}

// verbatimInside 標出 text 裡每一處 verbatim 文字的內部：回傳的切片以位元組位置為索引，true 表示這個位置的字元
// 跟前一個字元同屬一段照原樣送出的文字，兩者之間不補空格。每段的第一個字元不標（它跟前文之間照一般規則）。
// 沒有任何一段出現時回 nil。同一段文字出現幾次就標幾次：使用者在同一次送出裡另外手打了跟片語一模一樣的字，也照原樣。
func verbatimInside(text string, verbatim []string) []bool {
	var inside []bool
	for _, v := range verbatim {
		if v == "" {
			continue
		}
		for from := 0; from < len(text); {
			at := strings.Index(text[from:], v)
			if at < 0 {
				break
			}
			at += from
			if inside == nil {
				inside = make([]bool, len(text))
			}
			for i := at + 1; i < at+len(v); i++ {
				inside[i] = true
			}
			from = at + len(v)
		}
	}
	return inside
}

// isHan：漢字（unicode.Han，含擴充區與相容表意文字）；注音符號、假名、全形標點不算。
func isHan(r rune) bool { return r >= 0x2e80 && unicode.Is(unicode.Han, r) }

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }
