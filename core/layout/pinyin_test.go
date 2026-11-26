package layout

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// #324：漢語拼音鍵盤沒有注音鍵（字母、聲調由 session 收成拼音），Keys 是拼音加聲調數字，標點同許氏。
func TestPinyinLayout(t *testing.T) {
	if l, ok := ByName(NamePinyin); !ok || l != Pinyin || Pinyin.Name() != "pinyin" {
		t.Fatalf("ByName(pinyin)")
	}
	for _, k := range "abcxyz0123456789'," {
		if _, ok := Pinyin.Symbol(k); ok {
			t.Errorf("%q 不應是注音鍵", k)
		}
	}
	for syl, keys := range map[zhuyin.Syllable]string{
		"ㄋㄧˇ": "ni3", "ㄇㄚ": "ma1", "ㄉㄜ˙": "de5", "ㄓㄨㄥ": "zhong1", "ㄌㄩˋ": "lv4", "ㄦˊ": "er2", "ㄕ": "shi1",
		zhuyin.Untone("ㄒㄧㄢ"): "xian", "ㄝˊ": "",
	} {
		if got := Pinyin.Keys(syl); got != keys {
			t.Errorf("Keys(%s) = %q，應為 %q", syl, got, keys)
		}
	}
	for key, want := range map[rune]string{',': "，", '.': "。", ';': "；", '<': "，", '\'': "、", '\\': "、"} {
		if got, ok := Pinyin.Punct(key); !ok || got != want {
			t.Errorf("Punct(%q) = %q,%v，應為 %q", key, got, ok, want)
		}
	}
	if _, ok := Pinyin.Punct('/'); ok {
		t.Errorf("/ 不是標點，交還應用程式")
	}
}
