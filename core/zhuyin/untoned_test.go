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
