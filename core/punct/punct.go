// Package punct 定義提交時補標點的純資料契約：評分器只回傳「在第幾個字元前插入哪個標點」，
// 核心驗證後才套用；不改、不刪使用者的任何原字。模型載入、時間與取消由宿主負責。
package punct

import "unicode"

// Marks 是允許插入的標點。
const Marks = "，。？！、；"

// Edit 在原字串第 Offset 個字元（rune）之前插入 Text。
type Edit struct {
	Offset int    `json:"offset"`
	Text   string `json:"text"`
}

type Punctuator interface {
	Punctuate(text string) ([]Edit, error)
}

func allowed(mark string) bool {
	r := []rune(mark)
	if len(r) != 1 {
		return false
	}
	for _, m := range Marks {
		if r[0] == m {
			return true
		}
	}
	return false
}

// Apply 驗證後插入：位置嚴格遞增、在兩個漢字之間、不在開頭或段尾、只用 Marks。
// 任一編輯不合規就整批不套用（回傳原文與 false），避免部分套用造成意外結果。
func Apply(text string, edits []Edit) (string, bool) {
	if len(edits) == 0 {
		return text, false
	}
	r := []rune(text)
	last := 0
	for _, e := range edits {
		if e.Offset <= last || e.Offset >= len(r) || !allowed(e.Text) ||
			!unicode.Is(unicode.Han, r[e.Offset-1]) || !unicode.Is(unicode.Han, r[e.Offset]) {
			return text, false
		}
		last = e.Offset
	}
	out := make([]rune, 0, len(r)+len(edits))
	at := 0
	for i, c := range r {
		if at < len(edits) && edits[at].Offset == i {
			out = append(out, []rune(edits[at].Text)...)
			at++
		}
		out = append(out, c)
	}
	return string(out), true
}
