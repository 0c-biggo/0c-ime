package zhuyin

import (
	"slices"
	"strings"
	"testing"
)

func TestPinyinSyllable(t *testing.T) {
	for keys, want := range map[string]Syllable{
		"ni": "ㄋㄧ", "hao": "ㄏㄠ", "shi": "ㄕ", "jie": "ㄐㄧㄝ", "zhong": "ㄓㄨㄥ", "guo": "ㄍㄨㄛ", "ju": "ㄐㄩ",
		"lv": "ㄌㄩ", "nve": "ㄋㄩㄝ", "lue": "ㄌㄩㄝ", "nue": "ㄋㄩㄝ", "yuan": "ㄩㄢ", "yi": "ㄧ", "wu": "ㄨ",
		"er": "ㄦ", "zhuang": "ㄓㄨㄤ", "xiong": "ㄒㄩㄥ", "liu": "ㄌㄧㄡ", "gui": "ㄍㄨㄟ", "lun": "ㄌㄨㄣ", "jun": "ㄐㄩㄣ",
		"yai": "ㄧㄞ", "zhei": "ㄓㄟ", "a": "ㄚ", "e": "ㄜ", "o": "ㄛ",
	} {
		if got, ok := PinyinSyllable(keys); !ok || got != want {
			t.Errorf("PinyinSyllable(%q) = %q, %v；應為 %q", keys, got, ok, want)
		}
	}
	for _, bad := range []string{"", "n", "zh", "lü", "xa", "ba1", "bu'", "jv", "i", "u", "v", "ng", "ê"} {
		if got, ok := PinyinSyllable(bad); ok {
			t.Errorf("PinyinSyllable(%q) = %q，不應是音節", bad, got)
		}
	}
}

// 每個音節表內的音節（ê 除外）都打得出來，PinyinKeys 與 PinyinSyllable 互為反函數。
func TestPinyinKeysRoundTrip(t *testing.T) {
	for _, b := range Inventory() {
		k := PinyinKeys(b)
		if b == "ㄝ" {
			if k != "" {
				t.Errorf("ㄝ 不應有鍵盤拼法，得到 %q", k)
			}
			continue
		}
		if k == "" || strings.ContainsFunc(k, func(r rune) bool { return !PinyinLetter(r) }) {
			t.Errorf("%q 的鍵盤拼法 %q 應只有小寫字母", b, k)
			continue
		}
		if y, ok := PinyinSyllable(k); !ok || y != b {
			t.Errorf("PinyinSyllable(PinyinKeys(%q)=%q) = %q", b, k, y)
		}
		if len(k) > MaxPinyinLetters {
			t.Errorf("%q 超過 MaxPinyinLetters", k)
		}
	}
	if PinyinKeys("ㄋㄧˇ") != "ni" || PinyinKeys(Untone("ㄋㄧ")) != "ni" {
		t.Errorf("PinyinKeys 應忽略聲調與未打聲調記號")
	}
}

func TestPinyinStart(t *testing.T) {
	for _, k := range []string{"n", "zh", "zhu", "zhua", "zhuan", "zhuang", "x", "xia", "lv", "yo", "yon"} {
		if !PinyinStart(k) {
			t.Errorf("%q 應是音節開頭", k)
		}
	}
	for _, k := range []string{"i", "u", "v", "nih", "zhuangg", "bv", "x'"} {
		if PinyinStart(k) {
			t.Errorf("%q 不應是音節開頭", k)
		}
	}
}

func TestSplitPinyin(t *testing.T) {
	for keys, want := range map[string]string{
		"nihaoshijie":      "ni hao shi jie",
		"zhonghuaminguo":   "zhong hua min guo", // 不切成 ming uo（uo 不是音節）也不切成 ming guo…
		"xian":             "xian",              // 一個音節，不是 xi an
		"xi'an":            "xi an",             // 隔音符號
		"fangan":           "fan gan",           // 方案要打 fang'an（正詞法）
		"fang'an":          "fang an",
		"dier":             "di er", // 只有這種切法：a o e 開頭的照樣切得出來
		"tiane":            "tian e",
		"mingan":           "min gan",
		"xinan":            "xi nan",
		"zhuangzhuang":     "zhuang zhuang",
		"nideminzi":        "ni de min zi",
		"''ni''":           "ni",
		"womenyiqiqugongy": "",
		"zhognguo":         "",
		"hello":            "",
		"iphone":           "",
		"'":                "",
	} {
		got, ok := SplitPinyin(keys)
		if want == "" {
			if ok {
				t.Errorf("SplitPinyin(%q) = %q，應切不成", keys, got)
			}
			continue
		}
		if !ok || strings.Join(got, " ") != want {
			t.Errorf("SplitPinyin(%q) = %q, %v；應為 %q", keys, got, ok, want)
		}
	}
}

func TestPinyinCuts(t *testing.T) {
	got := PinyinCuts("xian")
	want := [][2]int{{0, 2}, {0, 3}, {0, 4}, {2, 3}, {2, 4}} // xi xia xian a an（n 不是音節）
	if !slices.Equal(got, want) {
		t.Errorf("PinyinCuts(xian) = %v，應為 %v", got, want)
	}
}
