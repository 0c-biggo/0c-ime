package zhuyin

import "testing"

func TestValid(t *testing.T) {
	for _, s := range []Syllable{"ㄓㄨˋ", "ㄇㄜ˙", "ㄔ", "ㄧ", "ㄩㄢˊ", "ㄦˋ", "ㄐㄧㄥˇ"} {
		if !s.Valid() {
			t.Errorf("%q 應為合法", s)
		}
	}
	for _, s := range []Syllable{"", "ˋ", "ㄨㄓ", "ㄓㄓ", "ㄓˋㄨ", "ㄓabc", "ㄓˉ"} {
		if s.Valid() {
			t.Errorf("%q 應為不合法", s)
		}
	}
}

func TestComposer(t *testing.T) {
	var c Composer
	for _, r := range "ㄐㄧㄥ" {
		if _, ok := c.Add(r); ok {
			t.Fatal("尚未輸入聲調不應完成")
		}
	}
	if got, ok := c.Add('ˇ'); !ok || got != "ㄐㄧㄥˇ" {
		t.Fatalf("got %q %v", got, ok)
	}
	if !c.Empty() {
		t.Fatal("完成後應清空")
	}
	// 同類別取代、順序無關、一聲不標
	c.Add('ㄧ')
	c.Add('ㄑ')
	c.Add('ㄐ')
	c.Add('ㄣ')
	if got, _ := c.Add(Tone1); got != "ㄐㄧㄣ" {
		t.Fatalf("got %q", got)
	}
	// 退格依輸入順序
	c.Add('ㄉ')
	c.Add('ㄨ')
	c.Backspace()
	if c.String() != "ㄉ" {
		t.Fatalf("got %q", c.String())
	}
	if _, ok := c.Add('ˋ'); !ok {
		t.Fatal("ㄉˋ 結構上可完成")
	}
	if _, ok := c.Add('ˋ'); ok {
		t.Fatal("空的組音器不應接受聲調")
	}
}
