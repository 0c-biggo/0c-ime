package layout

import (
	"testing"

	"github.com/0c-biggo/0c-ime/core/zhuyin"
)

func TestStandardRoundTrip(t *testing.T) {
	for syl, keys := range map[zhuyin.Syllable]string{
		"ㄑㄧㄣ": "fup ", "ㄗˇ": "y3", "ㄐㄧㄥˇ": "ru/3", "ㄉㄨㄥˋ": "2j/4", "ㄇㄚ˙": "a87", "ㄔ": "t ",
	} {
		if got := Standard.Keys(syl); got != keys {
			t.Errorf("Keys(%s)=%q 應為 %q", syl, got, keys)
		}
		var c zhuyin.Composer
		var done zhuyin.Syllable
		for _, k := range keys {
			sym, ok := Standard.Symbol(k)
			if !ok {
				t.Fatalf("按鍵 %q 無對應", k)
			}
			if s, fin := c.Add(sym); fin {
				done = s
			}
		}
		if done != syl {
			t.Errorf("按鍵 %q 組出 %q 應為 %q", keys, done, syl)
		}
	}
}

func TestStandardPunct(t *testing.T) {
	for key, want := range map[rune]string{'<': "，", '>': "。", '?': "？", '\'': "、", '\\': "、", '&': "＆", '^': "……", '_': "——"} {
		if got, ok := Standard.Punct(key); !ok || got != want {
			t.Errorf("Punct(%q)=%q,%v 應為 %q", key, got, ok, want)
		}
	}
}

// 倚天 41 鍵的完整對照（#83）：回報者提供的對照表，與新酷音 et.rs、小麥注音 CreateETenLayout 一致。
const etenTable = "bㄅpㄆmㄇfㄈdㄉtㄊnㄋlㄌvㄍkㄎhㄏgㄐ7ㄑcㄒ,ㄓ.ㄔ/ㄕjㄖ;ㄗ'ㄘsㄙeㄧxㄨuㄩaㄚoㄛrㄜwㄝiㄞqㄟzㄠyㄡ8ㄢ9ㄣ0ㄤ-ㄥ=ㄦ ˉ2ˊ3ˇ4ˋ1˙"

func TestETenTable(t *testing.T) {
	pairs := []rune(etenTable)
	if len(pairs) != 84 {
		t.Fatalf("對照表應有 42 筆，實際 %d", len(pairs)/2)
	}
	seen := map[rune]bool{}
	for i := 0; i < len(pairs); i += 2 {
		key, want := pairs[i], pairs[i+1]
		if got, ok := ETen.Symbol(key); !ok || got != want {
			t.Errorf("倚天 Symbol(%q)=%q,%v 應為 %q", key, got, ok, want)
		}
		seen[want] = true
	}
	if len(seen) != 42 {
		t.Errorf("倚天應涵蓋 37 個注音與 5 個聲調，實際 %d 個", len(seen))
	}
	for _, key := range "56`[]\\" {
		if sym, ok := ETen.Symbol(key); ok {
			t.Errorf("倚天的 %q 不是注音鍵，卻對到 %q", key, sym)
		}
	}
}

func TestETenRoundTrip(t *testing.T) {
	for syl, keys := range map[zhuyin.Syllable]string{
		"ㄑㄧㄣ": "7e9 ", "ㄗˇ": ";3", "ㄐㄧㄥˇ": "ge-3", "ㄉㄨㄥˋ": "dx-4", "ㄇㄚ˙": "ma1", "ㄔ": ". ",
		"ㄘˋ": "'4", "ㄦˊ": "=2", "ㄕˋ": "/4", "ㄓ": ", ", "ㄨㄛˇ": "xo3",
	} {
		if got := ETen.Keys(syl); got != keys {
			t.Errorf("Keys(%s)=%q 應為 %q", syl, got, keys)
		}
		var c zhuyin.Composer
		var done zhuyin.Syllable
		for _, k := range keys {
			sym, ok := ETen.Symbol(k)
			if !ok {
				t.Fatalf("按鍵 %q 無對應", k)
			}
			if s, fin := c.Add(sym); fin {
				done = s
			}
		}
		if done != syl {
			t.Errorf("按鍵 %q 組出 %q 應為 %q", keys, done, syl)
		}
	}
}

// 倚天的 ' 是 ㄘ，頓號改用 \；Shift 組合的標點與標準排列相同。
func TestETenPunct(t *testing.T) {
	for key, want := range map[rune]string{'<': "，", '>': "。", '?': "？", ':': "：", '"': "；", '\\': "、", '[': "「", ']': "」", '&': "＆", '^': "……", '_': "——"} {
		if got, ok := ETen.Punct(key); !ok || got != want {
			t.Errorf("Punct(%q)=%q,%v 應為 %q", key, got, ok, want)
		}
	}
	if p, ok := ETen.Punct('\''); ok {
		t.Errorf("倚天的 ' 是 ㄘ，不應是標點 %q", p)
	}
	// 標點不可與注音鍵重疊，否則那個標點永遠打不出來。
	for _, l := range []Layout{Standard, ETen} {
		for _, key := range "<>?:\"!~[]{}\\()&'^_" {
			if _, ok := l.Punct(key); ok {
				if sym, used := l.Symbol(key); used {
					t.Errorf("%s 的 %q 同時是注音 %q 與標點", l.Name(), key, sym)
				}
			}
		}
	}
}

func TestByName(t *testing.T) {
	if l, ok := ByName("standard"); !ok || l != Standard {
		t.Error("standard 應為標準排列")
	}
	if l, ok := ByName("eten"); !ok || l != ETen {
		t.Error("eten 應為倚天 41 鍵")
	}
	if l, ok := ByName("hsu"); !ok || l != Hsu {
		t.Error("hsu 應為許氏排列")
	}
	if _, ok := ByName("eten26"); ok {
		t.Error("不認得的排列應回傳 false")
	}
}
