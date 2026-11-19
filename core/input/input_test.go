package input

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func abbrev(p string) zhuyin.Syllable { return zhuyin.Abbreviate(zhuyin.Syllable(p), 0) }

func costOf(row []Hyp, y zhuyin.Syllable) (float64, bool) {
	for _, h := range row {
		if h.Syl == y {
			return h.Cost, true
		}
	}
	return 0, false
}

func TestAbbreviationCostFollowsTheRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		syls []zhuyin.Syllable
		at   int
		want float64
	}{
		{"旁邊是完整音節：ㄕ 多半就是 ㄕ", []zhuyin.Syllable{zhuyin.Untone("ㄧㄠ"), abbrev("ㄕ")}, 1, AbbreviationCost},
		{"連續的單音節也不算確定在簡拼", []zhuyin.Syllable{abbrev("ㄕ"), abbrev("ㄗ")}, 0, AbbreviationCost},
		{"有不成音節的聲母才確定在簡拼", []zhuyin.Syllable{abbrev("ㄕ"), abbrev("ㄐ")}, 0, AbbreviationRunCost},
		{"整串都算", []zhuyin.Syllable{abbrev("ㄕ"), abbrev("ㄗ"), abbrev("ㄐ")}, 0, AbbreviationRunCost},
		{"被完整音節隔開就不算", []zhuyin.Syllable{abbrev("ㄕ"), zhuyin.Untone("ㄧㄠ"), abbrev("ㄐ")}, 0, AbbreviationCost},
	} {
		lat := Exact{}.Expand(tc.syls)
		if got, ok := costOf(lat[tc.at], "ㄕㄤˋ"); !ok || got != tc.want {
			t.Errorf("%s：ㄕㄤˋ 成本 %v（有 %v），要 %v", tc.name, got, ok, tc.want)
		}
		if got, ok := costOf(lat[tc.at], "ㄕˋ"); !ok || got != 0 {
			t.Errorf("%s：打的音節本身成本 %v（有 %v），要 0", tc.name, got, ok)
		}
	}
}

func TestAbbreviationOfNonSyllableInitialIsFree(t *testing.T) {
	for _, h := range (Exact{}).Expand([]zhuyin.Syllable{abbrev("ㄋ")})[0] {
		if h.Cost != 0 || !h.Untoned {
			t.Fatalf("ㄋ 的展開由語言模型決定：%+v", h)
		}
	}
}
