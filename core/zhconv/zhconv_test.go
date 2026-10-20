package zhconv

import "testing"

// 迷你字典：真正的上游字典與 OpenCC 的對照在 internal/simplified。
func tiny(t *testing.T) *Converter {
	t.Helper()
	c, err := TW2S(Data{
		CJKCompatibility:     "# 相容表意字\n" + "\uF900\t豈\n",
		TWVariants:           "着\t著\n樑\t梁 樑\n# @reverse-prefer: 梁\n梁\t梁\n",
		TWVariantsRevPhrases: "什么\t什麼\n顯著\t顯著\n",
		TSPhrases:            "顯著\t显著\n計畫\t计划\n著名\t著名\n",
		TSCharacters:         "顯\t显\n計\t计\n畫\t画 划\n麼\t么 麽\n乾\t干 乾\n梁\t梁\n名\t名\n豈\t岂\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConvertFollowsOpenCCMatching(t *testing.T) {
	c := tiny(t)
	for in, want := range map[string]string{
		"顯著":       "显著", // TWVariantsRevPhrases 整詞保護，不是逐字的「显着」
		"看著":       "看着", // TWVariantsRev（着←著）後再 TSCharacters
		"計畫":       "计划",
		"畫":        "画", // 多個值取第一個
		"什麼":       "什么",
		"梁":        "梁", // @reverse-prefer：梁 的反轉取「梁」不取「樑」
		"\uF900":   "岂", // 先正規化相容表意字
		"abc 123！": "abc 123！",
		"":         "",
	} {
		if got := c.Convert(in); got != want {
			t.Errorf("%q → %q，預期 %q", in, got, want)
		}
	}
}

func TestPinnedPositionsKeepTheirOutputAndSplitSegments(t *testing.T) {
	c := tiny(t)
	// 「著」讀 ㄓㄨˋ：固定輸出「著」；也是分段邊界，「顯著」不再整詞比對（結果一樣是「显著」）。
	if got := c.ConvertPinned("顯著看著", map[int]string{1: "著"}); got != "显著看着" {
		t.Fatalf("%q", got)
	}
	if got := c.ConvertPinned("乾乾", map[int]string{0: "乾"}); got != "乾干" {
		t.Fatalf("%q", got)
	}
	if got := c.ConvertPinned("乾", map[int]string{5: "乾"}); got != "干" { // 超出範圍的位置不影響
		t.Fatalf("%q", got)
	}
}

func TestPinTable(t *testing.T) {
	for _, tc := range []struct {
		c       rune
		reading string
		want    string
		ok      bool
	}{
		{'著', "ㄓㄨˋ", "著", true},
		{'著', "ㄓㄜ˙", "", false}, // 交給詞表：「顯著」也收了 ㄒㄧㄢˇ ㄓㄜ˙
		{'乾', "ㄑㄧㄢˊ", "乾", true},
		{'乾', "ㄍㄢ", "干", true},
		{'瞭', "ㄌㄧㄠˇ", "了", true},
		{'瞭', "ㄌㄧㄠˋ", "瞭", true},
		{'沈', "ㄔㄣˊ", "沉", true},
		{'沈', "ㄕㄣˇ", "沈", true},
		{'畫', "ㄏㄨㄚˋ", "", false},
	} {
		if got, ok := Pin(tc.c, tc.reading); got != tc.want || ok != tc.ok {
			t.Errorf("%c %s → %q %v", tc.c, tc.reading, got, ok)
		}
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := ParseDict("沒有分隔\n"); err == nil {
		t.Fatal("缺 Tab 應報錯")
	}
	if _, err := ParseReversed("# @reverse-prefer: 梁 樑\n梁\t梁\n"); err == nil {
		t.Fatal("指定的反轉沒有對應詞條應報錯")
	}
	if _, err := ParseReversed("# @reverse-prefer: 无\n樑\t梁\n"); err == nil {
		t.Fatal("指定的值不存在應報錯")
	}
	if _, err := TW2S(Data{}); err == nil {
		t.Fatal("空字典應報錯")
	}
}
