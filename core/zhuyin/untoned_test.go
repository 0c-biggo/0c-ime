package zhuyin

import (
	"slices"
	"testing"
)

func TestUntoned(t *testing.T) {
	y := Untone("ㄋㄧ")
	if !y.Untoned() || y.Valid() || y.Legal() || y.ToneOf() != 0 || y.Base() != "ㄋㄧ" || y.Text() != "ㄋㄧ" {
		t.Fatalf("%q：Untoned %v Valid %v Legal %v ToneOf %d Base %q Text %q", y, y.Untoned(), y.Valid(), y.Legal(), y.ToneOf(), y.Base(), y.Text())
	}
	if Untone("ㄋㄧˇ") != y {
		t.Fatal("Untone 去掉原本的聲調")
	}
	if want := []Syllable{"ㄋㄧ", "ㄋㄧˊ", "ㄋㄧˇ", "ㄋㄧˋ", "ㄋㄧ˙"}; !slices.Equal(y.Tones(), want) {
		t.Fatalf("五個聲調：%v", y.Tones())
	}
	for _, s := range []Syllable{"ㄋㄧ", "ㄋㄧˇ", "?", "ˇ?", "ㄋㄧˇ?", ""} {
		if s.Untoned() {
			t.Errorf("%q 不是沒打聲調的音節", s)
		}
		if s != "" && !slices.Equal(s.Tones(), []Syllable{s}) {
			t.Errorf("%q 的 Tones 只有自己", s)
		}
	}
	if Syllable("?").Base() != "?" || Syllable("?").Text() != "?" {
		t.Fatal("字面的 ? 不受影響")
	}
	if !HasUntoned([]Syllable{"ㄨㄛˇ", y}) || HasUntoned([]Syllable{"ㄨㄛˇ", "ㄋㄧ"}) {
		t.Fatal("HasUntoned")
	}
}

func TestLegalPrefix(t *testing.T) {
	for _, s := range []Syllable{"ㄒ", "ㄒㄧ", "ㄒㄧㄢ", "ㄓㄨ", "ㄢ", "ㄧ", "ㄧㄢ", "ㄈㄧ"} {
		if !LegalPrefix(s) {
			t.Errorf("%q 是音節的開頭", s)
		}
	}
	for _, s := range []Syllable{"ㄉㄧㄤ", "ㄅㄩ", "ㄒㄚ", "ㄒㄧˋ", ""} {
		if LegalPrefix(s) {
			t.Errorf("%q 不是任何音節的開頭", s)
		}
	}
}

func TestAbbreviation(t *testing.T) {
	y := Abbreviate("ㄋ", 0)
	if !y.Abbreviated() || !y.Untoned() || y.Valid() || y.Legal() || y.ToneOf() != 0 || y.Base() != "ㄋ" || y.Text() != "ㄋ" {
		t.Fatalf("%q：Abbreviated %v Untoned %v Valid %v Legal %v ToneOf %d Base %q Text %q", y, y.Abbreviated(), y.Untoned(), y.Valid(), y.Legal(), y.ToneOf(), y.Base(), y.Text())
	}
	all := y.Tones()
	for _, want := range []Syllable{"ㄋㄧˇ", "ㄋㄚ", "ㄋㄩˇ", "ㄋㄨㄥˊ"} {
		if !slices.Contains(all, want) {
			t.Errorf("ㄋ 展開缺 %q", want)
		}
	}
	for _, s := range all {
		if !s.Legal() || s.Base()[:len("ㄋ")] != "ㄋ" {
			t.Errorf("展開出不合法或不同聲母的 %q", s)
		}
	}
	three := Abbreviate("ㄅ", 3)
	if three.ToneOf() != 3 || three.Text() != "ㄅˇ" {
		t.Fatalf("%q ToneOf %d Text %q", three, three.ToneOf(), three.Text())
	}
	for _, s := range three.Tones() {
		if s.ToneOf() != 3 {
			t.Errorf("ㄅˇ 只展開三聲：%q", s)
		}
	}
	if one := Abbreviate("ㄅ", 1); one.Text() != "ㄅ" || one.ToneOf() != 1 || one == Abbreviate("ㄅ", 0) {
		t.Fatalf("一聲的簡拼不標聲調但仍限一聲：%q", one)
	}
	if z := Abbreviate("ㄓ", 0).Tones(); z[0] != "ㄓ" {
		t.Fatalf("打的注音本身是完整音節時排第一個：%v", z[:5])
	}
	if Abbreviate("ㄚ", 0) != "" || Abbreviate("ㄋㄧ", 0) != "" || Abbreviate("ㄋ", 6) != "" {
		t.Fatal("只有單一聲母或介音能簡拼")
	}
	for _, s := range []Syllable{"ㄋ", "*", "ㄋ*ㄧ", "ㄋ*ˇˇ", "ㄚ*", "ㄋㄧ*"} {
		if s.Abbreviated() {
			t.Errorf("%q 不是簡拼音節", s)
		}
	}
	if !HasUntoned([]Syllable{"ㄨㄛˇ", three}) {
		t.Fatal("HasUntoned 包含簡拼")
	}
}
