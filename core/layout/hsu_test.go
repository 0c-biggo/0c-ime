package layout

import (
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

// hsuType 依許氏逐鍵組字，回傳完成的音節與最後還沒完成的注音。
func hsuType(t *testing.T, keys string) ([]zhuyin.Syllable, string) {
	t.Helper()
	var c zhuyin.Composer
	var out []zhuyin.Syllable
	for _, k := range keys {
		next, sym, ok := Hsu.Press(c, k)
		if !ok {
			t.Fatalf("許氏 %q 的 %q 不是注音鍵", keys, k)
		}
		if zhuyin.ClassOf(sym) != zhuyin.Tone {
			c = next
			continue
		}
		if c.Empty() {
			continue // 空白在沒有注音時不是一聲
		}
		done, ok := next.Add(sym)
		if !ok {
			t.Fatalf("許氏 %q 的聲調 %q 沒有完成音節", keys, k)
		}
		out = append(out, done)
		c = zhuyin.Composer{}
	}
	return out, c.String()
}

func reading(syls []zhuyin.Syllable) string { return zhuyin.Key(syls) }

// 新酷音 libchewing 的許氏測試（src/editor/zhuyin_layout/hsu.rs 與 tests/test-bopomofo.c 的 test_KB_HSU、
// test_KB_HSU_JVC、test_KB_HSU_example）：按鍵與注音逐字照抄，字（新酷音真的很棒…）換成讀音。
func TestHsuLibchewingCases(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"cen ", "ㄒㄧㄣ"}, // hsu.rs cen：c 接 ㄧ 是 ㄒ
		{"nf", "ㄣˇ"},    // hsu.rs convert_n_to_en
		{"cen kxjen jn dgshnfbkj", "ㄒㄧㄣ ㄎㄨˋ ㄧㄣ ㄓㄣ ㄉㄜ˙ ㄏㄣˇ ㄅㄤˋ"}, // 新酷音真的很棒
		{"m ", "ㄢ"},                           // ㄇ 加聲調改成 ㄢ：安
		{"hd", "ㄛˊ"},                          // 哦
		{"g ", "ㄜ"},                           // 婀
		{"k ", "ㄤ"},                           // 腌
		{"lf", "ㄦˇ"},                          // 耳
		{"ge ", "ㄐㄧ"},                         // ㄍㄧ 改成 ㄐㄧ：機
		{"gen ", "ㄐㄧㄣ"},                       // 今
		{"geej", "ㄐㄧㄝˋ"},                      // 藉
		{"gu ", "ㄐㄩ"},                         // 居
		{"gued", "ㄐㄩㄝˊ"},                      // 決
		{"j ", "ㄓ"}, {"v ", "ㄔ"}, {"c ", "ㄕ"}, // 之 吃 失
		{"bnfjxl cen deljudmeldrjki jk ", "ㄅㄣˇ ㄓㄨㄥ ㄒㄧㄣ ㄉㄧㄥˋ ㄩˊ ㄇㄧㄥˊ ㄖˋ ㄎㄞ ㄓㄤ"},      // 本中心訂於明日開張
		{"tidxm dgsrndgxl hnfgxaj", "ㄊㄞˊ ㄨㄢ ㄉㄜ˙ ㄖㄣˊ ㄍㄨㄥ ㄏㄣˇ ㄍㄨㄟˋ"},                  // 台灣的人工很貴
		{"jeofuejcdrjceyjxfljcd", "ㄐㄧㄡˇ ㄩㄝˋ ㄕˊ ㄖˋ ㄒㄧㄚˋ ㄨˇ ㄦˋ ㄕˊ"},                   // 九月十日下午二時
		{"e j j kgfijdgscewfhxy mw ", "ㄧ ㄓ ㄓ ㄎㄜˇ ㄞˋ ㄉㄜ˙ ㄒㄧㄠˇ ㄏㄨㄚ ㄇㄠ"},               // 一隻隻可愛的小花貓
		{"ceflgse ggshnfcx fxddgszwf", "ㄒㄧˇ ㄌㄜ˙ ㄧ ㄍㄜ˙ ㄏㄣˇ ㄕㄨ ㄈㄨˊ ㄉㄜ˙ ㄗㄠˇ"},          // 洗了一個很舒服的澡
		{"tidbafcjcje ggsmaflejdgsvldcj", "ㄊㄞˊ ㄅㄟˇ ㄕˋ ㄕˋ ㄧ ㄍㄜ˙ ㄇㄟˇ ㄌㄧˋ ㄉㄜ˙ ㄔㄥˊ ㄕˋ"}, // 台北市是一個美麗的城市
	} {
		syls, pending := hsuType(t, c.keys)
		if got := reading(syls); got != c.want || pending != "" {
			t.Errorf("許氏 %q 組出 %q（未完成 %q），應為 %q", c.keys, got, pending, c.want)
		}
	}
}

// 按鍵順序不必照注音順序（新酷音 test_KB_HSU_JVC 的 cek、cke），還沒按聲調時的顯示也照新酷音。
func TestHsuPendingSymbols(t *testing.T) {
	for _, c := range []struct{ keys, want string }{
		{"m", "ㄇ"}, {"h", "ㄏ"}, {"g", "ㄍ"}, {"n", "ㄋ"}, {"k", "ㄎ"}, {"l", "ㄌ"},
		{"ge", "ㄍㄧ"},   // 下一鍵才改成 ㄐ
		{"gen", "ㄐㄧㄣ"}, //
		{"gee", "ㄐㄧㄝ"}, //
		{"gue", "ㄐㄩㄝ"}, //
		{"cek", "ㄒㄧㄤ"}, //
		{"cke", "ㄒㄧㄤ"}, // ㄕㄤ 再接 ㄧ 改成 ㄒ
		{"jx", "ㄓㄨ"},   //
		{"jex", "ㄓㄨ"},  // ㄐㄧ 的介音換成 ㄨ，聲母改回 ㄓ
		{"ec", "ㄒㄧ"},   // 介音先打再補 c：ㄒ（威注音的做法；新酷音留下 ㄕㄧ）
		{"ev", "ㄑㄧ"},   //
		{"ba", "ㄅㄟ"},   // 有聲母時 a 是 ㄟ
		{"xa", "ㄨㄟ"},   // 有介音時也是
		{"ay", "ㄘㄚ"},   // 只有韻母時 a 仍是聲母 ㄘ
		{"ee", "ㄧㄝ"},   // 有介音時 e 是 ㄝ
		{"bm", "ㄅㄢ"},   //
		{"ll", "ㄌㄥ"},   //
	} {
		syls, pending := hsuType(t, c.keys)
		if len(syls) != 0 || pending != c.want {
			t.Errorf("許氏 %q 應顯示 %q，實際 %q（已完成 %v）", c.keys, c.want, pending, syls)
		}
	}
}

// 退格退掉最後一鍵，順便改掉的聲母一起還原（新酷音 test_KB_HSU_JVC：cke 退兩次再按 k 是 ㄕㄤ）。
func TestHsuBackspaceReplaysKeys(t *testing.T) {
	press := func(c zhuyin.Composer, keys string) zhuyin.Composer {
		for _, k := range keys {
			c, _, _ = Hsu.Press(c, k)
		}
		return c
	}
	c := press(zhuyin.Composer{}, "cke")
	if c.String() != "ㄒㄧㄤ" {
		t.Fatalf("cke 應為 ㄒㄧㄤ，實際 %q", c.String())
	}
	c = Hsu.Backspace(c)
	if c.String() != "ㄕㄤ" {
		t.Errorf("cke 退一格應回到 ck 的 ㄕㄤ（ㄒ 還原成 ㄕ），實際 %q", c.String())
	}
	c = press(Hsu.Backspace(c), "k")
	if c.String() != "ㄕㄤ" {
		t.Errorf("再退一格再按 k 應為 ㄕㄤ，實際 %q", c.String())
	}
	for _, c := range []struct{ keys, want string }{
		{"je", "ㄓ"}, // ㄐㄧ 退一格：j 單獨是 ㄓ
		{"ge", "ㄍ"}, //
		{"gen", "ㄍㄧ"},
		{"ba", "ㄅ"},
		{"b", ""},
	} {
		back := Hsu.Backspace(press(zhuyin.Composer{}, c.keys))
		if got := back.String(); got != c.want {
			t.Errorf("%q 退一格應為 %q，實際 %q", c.keys, c.want, got)
		}
	}
	// 狀態不是由按鍵組成時（沒有記錄）照一般規則退掉最後一個符號。
	var raw zhuyin.Composer
	raw.Add('ㄒ')
	raw.Add('ㄧ')
	if back := Hsu.Backspace(raw); back.String() != "ㄒ" {
		got := back.String()
		t.Errorf("沒有按鍵記錄時退格應退掉 ㄧ，實際 %q", got)
	}
	if raw.String() != "ㄒㄧ" {
		t.Errorf("Backspace 不可修改傳入的狀態：%q", raw.String())
	}
}

// 讀音→按鍵→讀音：音節表裡每個音節的五個聲調都能用 Keys 打回來；只有 ㄟ ㄝ ㄥ 單獨成音節時
// 打出的是同一串按鍵的預設讀音 ㄘ ㄧ ㄦ，Readings 列得到原本的讀音。
func TestHsuRoundTripAllSyllables(t *testing.T) {
	n := 0
	for _, base := range zhuyin.Inventory() {
		for tone := 1; tone <= 5; tone++ {
			syl := base.WithTone(tone)
			keys := Hsu.Keys(syl)
			syls, pending := hsuType(t, keys)
			if len(syls) != 1 || pending != "" {
				t.Errorf("%s 的按鍵 %q 組出 %v（未完成 %q）", syl, keys, syls, pending)
				continue
			}
			got := syls[0]
			if got != syl {
				alts := Hsu.Readings(got)
				if len(alts) < 2 || alts[0] != got || !strings.Contains(zhuyin.Key(alts[1:]), string(syl)) {
					t.Errorf("%s 的按鍵 %q 組出 %s", syl, keys, got)
				}
			}
			n++
		}
	}
	if n < 2000 {
		t.Fatalf("音節表太小：%d", n)
	}
}

func TestHsuReadings(t *testing.T) {
	for syl, want := range map[zhuyin.Syllable]string{
		"ㄘˋ": "ㄘˋ ㄟˋ", "ㄟˋ": "ㄘˋ ㄟˋ", "ㄘ": "ㄘ ㄟ", "ㄧ": "ㄧ ㄝ", "ㄧˊ": "ㄧˊ ㄝˊ", "ㄦˋ": "ㄦˋ ㄥˋ", "ㄥ": "ㄦ ㄥ",
		"ㄘㄞˋ": "", "ㄅㄧ": "", "ㄓ": "", "ㄢ": "",
	} {
		if got := zhuyin.Key(Hsu.Readings(syl)); got != want {
			t.Errorf("Readings(%s)=%q 應為 %q", syl, got, want)
		}
	}
	if Standard.Readings("ㄘˋ") != nil || ETen.Readings("ㄧ") != nil {
		t.Error("一鍵一符號的排列沒有歧義讀音")
	}
}

func TestHsuKeysAndPunct(t *testing.T) {
	// 暫存是空的時候 d f j s 與空白不是聲調；不是字母的鍵都不是注音鍵。
	for key, want := range map[rune]rune{'d': 'ㄉ', 'f': 'ㄈ', 'j': 'ㄓ', 's': 'ㄙ', 'a': 'ㄘ', 'e': 'ㄧ', 'l': 'ㄌ', ' ': zhuyin.Tone1} {
		if got, ok := Hsu.Symbol(key); !ok || got != want {
			t.Errorf("Symbol(%q)=%q,%v 應為 %q", key, got, ok, want)
		}
	}
	for _, key := range "1234567890-=/,.;'[]\\`AZ" {
		if _, ok := Hsu.Symbol(key); ok {
			t.Errorf("%q 不是許氏的注音鍵", key)
		}
	}
	for key, want := range map[rune]string{',': "，", '.': "。", ';': "；", '\'': "、", '\\': "、", '<': "，", '>': "。", '?': "？", '_': "——"} {
		if got, ok := Hsu.Punct(key); !ok || got != want {
			t.Errorf("Punct(%q)=%q,%v 應為 %q", key, got, ok, want)
		}
	}
	for _, key := range "1-/=" {
		if _, ok := Hsu.Punct(key); ok {
			t.Errorf("%q 在許氏不是標點", key)
		}
	}
	if l, ok := ByName("hsu"); !ok || l != Hsu || l.Name() != "hsu" {
		t.Error("ByName(hsu)")
	}
	// 標準排列的標點表不能被許氏的表改到。
	if _, ok := Standard.Punct(','); ok {
		t.Error("標準排列的 , 是 ㄝ，不是標點")
	}
}

// 靜態排列的 Press 與 Symbol 一致，不修改傳入的狀態。
func TestStaticPress(t *testing.T) {
	var c zhuyin.Composer
	c.Add('ㄅ')
	next, sym, ok := Standard.Press(c, 'u')
	if !ok || sym != 'ㄧ' || next.String() != "ㄅㄧ" || c.String() != "ㄅ" {
		t.Errorf("Press(ㄅ, u)=%q,%q,%v；原狀態 %q", next.String(), sym, ok, c.String())
	}
	next, sym, ok = Standard.Press(next, '3')
	if !ok || sym != 'ˇ' || next.String() != "ㄅㄧ" {
		t.Errorf("聲調鍵應回傳未加聲調的狀態：%q,%q,%v", next.String(), sym, ok)
	}
	if back := Standard.Backspace(next); back.String() != "ㄅ" || next.String() != "ㄅㄧ" {
		t.Errorf("Backspace=%q", back.String())
	}
}
