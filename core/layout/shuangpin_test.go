package layout

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestXiaohePairs(t *testing.T) {
	for _, c := range []struct{ pair, want string }{
		{"ni", "ni"}, {"hc", "hao"}, {"ui", "shi"}, {"jp", "jie"}, {"vs", "zhong"}, {"hx", "hua"},
		{"mb", "min"}, {"go", "guo"}, {"de", "de"}, {"mk", "ming"}, {"zi", "zi"}, {"xm", "xian"},
		{"lv", "lv"}, {"nv", "nv"}, {"lt", "lve"}, {"nt", "nve"}, {"jt", "jue"}, {"wo", "wo"}, {"ai", "ai"},
		{"aa", "a"}, {"oo", "o"}, {"ee", "e"}, {"an", "an"}, {"ah", "ang"}, {"eg", "eng"}, {"er", "er"}, {"ou", "ou"},
		{"aj", "an"}, {"ac", "ao"}, // 韻母鍵的寫法也收
		{"gk", "guai"}, {"jk", "jing"}, {"gl", "guang"}, {"jl", "jiang"}, {"js", "jiong"}, {"ds", "dong"},
		{"gx", "gua"}, {"jx", "jia"}, {"gv", "gui"}, {"ir", "chuan"}, {"uy", "shun"}, {"vl", "zhuang"},
		{"yj", "yan"}, {"yc", "yao"}, {"yz", "you"}, {"yb", "yin"}, {"yk", "ying"}, {"ys", "yong"},
		{"yu", "yu"}, {"yt", "yue"}, {"yr", "yuan"}, {"yy", "yun"}, {"wh", "wang"}, {"wg", "weng"}, {"ww", "wei"},
		{"lo", "luo"}, // lo（咯）與 luo 同兩鍵，取常用的 luo
		{"vi", "zhi"}, {"ii", "chi"}, {"ri", "ri"}, {"ci", "ci"}, {"si", "si"},
	} {
		got, ok := Xiaohe.Pair(c.pair)
		if !ok || got[0] != c.want {
			t.Errorf("小鶴 %q 應為 %q，實際 %v（ok=%v）", c.pair, c.want, got, ok)
		}
	}
	for _, bad := range []string{"bv", "fq", "ax", "qa", "fk"} {
		if got, ok := Xiaohe.Pair(bad); ok {
			t.Errorf("%q 不是音節，卻得到 %v", bad, got)
		}
	}
}

// 音節表裡（除了跟 luan 同兩鍵的 lüan）每個音節都打得出來，而且 Keys 打出來的兩鍵換回同一個音節。
func TestXiaoheCoversInventory(t *testing.T) {
	for _, p := range zhuyin.PinyinInventory() {
		y, _ := zhuyin.PinyinSyllable(p)
		k := Xiaohe.Keys(zhuyin.Untone(y))
		if k == "" {
			if p != "lvan" {
				t.Errorf("%s（%s）打不出來", p, y)
			}
			continue
		}
		got, ok := Xiaohe.Pair(k)
		if !ok {
			t.Fatalf("%s 的兩鍵 %q 換不回音節", p, k)
		}
		if back, _ := zhuyin.PinyinSyllable(got[0]); back != y && p != "lo" {
			t.Errorf("%s 的兩鍵 %q 換回 %s", p, k, got[0])
		}
	}
	if k := Xiaohe.Keys(zhuyin.Syllable("ㄏㄠˇ")); k != "hc3" {
		t.Errorf("ㄏㄠˇ 的按鍵 %q，應為 hc3", k)
	}
}

func TestXiaoheToPinyin(t *testing.T) {
	for _, c := range []struct {
		keys, want string
		ok         bool
	}{
		{"nihc", "ni'hao", true},
		{"vsmb", "zhong'min", true},
		{"xian", "xi'an", true}, // 雙拼沒有切法歧義
		{"xm", "xian", true},
		{"nih", "", false},  // 落單的一鍵
		{"nibv", "", false}, // bv 拼不成
		{"ni'hc", "ni'hao", true},
	} {
		got, ok := Xiaohe.ToPinyin(c.keys)
		if ok != c.ok || got != c.want {
			t.Errorf("ToPinyin(%q) = %q, %v；應為 %q, %v", c.keys, got, ok, c.want, c.ok)
		}
	}
	if p := Xiaohe.Preview("nihcm"); p != "ni hao m" {
		t.Errorf("Preview %q", p)
	}
	if l, ok := ByName(NameXiaohe); !ok || l != Layout(Xiaohe) || !PinyinFamily(l) {
		t.Error("ByName(xiaohe)")
	}
}
