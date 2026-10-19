package zhuyin

import "testing"

func TestFromPinyin(t *testing.T) {
	for py, want := range map[string]Syllable{
		"qi3": "ㄑㄧˇ", "ye4": "ㄧㄝˋ", "shuo1": "ㄕㄨㄛ", "shui4": "ㄕㄨㄟˋ", "dong4": "ㄉㄨㄥˋ",
		"jing3": "ㄐㄧㄥˇ", "zhi1": "ㄓ", "si4": "ㄙˋ", "ri4": "ㄖˋ", "er2": "ㄦˊ", "r5": "ㄦ˙",
		"lu:e4": "ㄌㄩㄝˋ", "nü3": "ㄋㄩˇ", "lv4": "ㄌㄩˋ", "ju2": "ㄐㄩˊ", "que4": "ㄑㄩㄝˋ",
		"xuan1": "ㄒㄩㄢ", "jun1": "ㄐㄩㄣ", "qiong2": "ㄑㄩㄥˊ", "yong3": "ㄩㄥˇ", "yun2": "ㄩㄣˊ",
		"yue4": "ㄩㄝˋ", "you3": "ㄧㄡˇ", "liu2": "ㄌㄧㄡˊ", "gui4": "ㄍㄨㄟˋ", "dun4": "ㄉㄨㄣˋ",
		"weng1": "ㄨㄥ", "wo3": "ㄨㄛˇ", "hong2": "ㄏㄨㄥˊ", "bo2": "ㄅㄛˊ", "me5": "ㄇㄜ˙", "ma": "ㄇㄚ˙",
		"a1": "ㄚ", "e4": "ㄜˋ", "ê2": "ㄝˊ", "yai2": "ㄧㄞˊ", "zhei4": "ㄓㄟˋ",
		"qǐ": "ㄑㄧˇ", "lǜ": "ㄌㄩˋ", "qi\u030c": "ㄑㄧˇ", "lu\u0308\u0300": "ㄌㄩˋ", "shuō": "ㄕㄨㄛ", "nǚ": "ㄋㄩˇ", "ér": "ㄦˊ",
	} {
		got, err := FromPinyin(py)
		if err != nil || got != want {
			t.Errorf("FromPinyin(%q) = %q, %v；應為 %q", py, got, err, want)
		}
	}
	for _, bad := range []string{"bu:3", "xa1", "zhia4", "abc", "q", "hm5", "ng2", "12"} {
		if got, err := FromPinyin(bad); err == nil {
			t.Errorf("FromPinyin(%q) = %q，應回報錯誤", bad, got)
		}
	}
}

func TestLegal(t *testing.T) {
	for _, s := range []Syllable{"ㄉㄨㄥˋ", "ㄐㄧㄥˇ", "ㄓ", "ㄇㄜ˙", "ㄦˊ", "ㄩㄥˇ", "ㄧㄞˊ", "ㄋㄩㄝˋ", "ㄌㄩㄢˊ", "ㄙㄟ", "ㄆㄧㄚ"} {
		if !s.Legal() {
			t.Errorf("%q 應為合法音節", s)
		}
	}
	for _, s := range []Syllable{"ㄅㄩ", "ㄓㄧ", "ㄍㄧ", "ㄈㄧㄥ", "ㄉㄨㄥx", "ㄦㄢ"} {
		if s.Legal() {
			t.Errorf("%q 應不合法", s)
		}
	}
	if n := len(legalBases); n < 400 || n > 430 {
		t.Errorf("音節表有 %d 個基本音節，與標準的約 410 個差太多", n)
	}
}

func TestToneHelpers(t *testing.T) {
	s := Syllable("ㄧ")
	if s.ToneOf() != 1 || s.WithTone(2) != "ㄧˊ" || Syllable("ㄧˋ").WithTone(1) != "ㄧ" || Syllable("ㄇㄜ˙").ToneOf() != 5 {
		t.Error("聲調輔助函式")
	}
}
