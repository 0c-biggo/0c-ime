package personal

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// 聲調選填（#150）：更正紀錄存詞庫讀音；之後不打聲調打同一串時，同基底的音節相容，紀錄照樣生效。
// 有打聲調的位置仍要相同；左右鄰也一樣比。
func TestUntonedSyllablesMatchStoredCorrections(t *testing.T) {
	m := evidenceModel()
	m.Learn(zaiCorrection("在", "ㄏㄞˇ"))
	for _, typed := range [][]zhuyin.Syllable{
		{zhuyin.Untone("ㄗㄞ"), zhuyin.Untone("ㄏㄞ")},
		{zhuyin.Untone("ㄗㄞ"), "ㄏㄞˇ"},
		{"ㄗㄞˋ", zhuyin.Untone("ㄏㄞ")},
	} {
		p := m.CorrectionPenalty(typed, nil)
		if p == nil || p(0, 1, "再") <= 0 || p(0, 1, "在") != 0 {
			t.Fatalf("%v：相容的讀音與右鄰應使用更正", typed)
		}
		if u := m.CorrectionUsage(typed); u == nil || u(0, "在") <= 0 {
			t.Fatalf("%v：補全證據", typed)
		}
	}
	for _, typed := range [][]zhuyin.Syllable{
		{zhuyin.Untone("ㄗㄞ"), "ㄏㄞˋ"},               // 有打的右鄰聲調不同
		{"ㄗㄞˇ", zhuyin.Untone("ㄏㄞ")},               // 有打的聲調不同
		{zhuyin.Untone("ㄗㄞ")},                      // 右邊沒有字
		{zhuyin.Untone("ㄗㄠ"), zhuyin.Untone("ㄏㄞ")}, // 基底不同
	} {
		if p := m.CorrectionPenalty(typed, nil); p != nil && p(0, 1, "再") > 0 {
			t.Fatalf("%v：不相容的不能套用", typed)
		}
	}
}
