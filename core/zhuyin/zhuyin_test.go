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

// 許氏（#183）的組音操作：Replace 換符號不改輸入順序、Remove 移除一類，兩者與 Add、Backspace 都清掉按鍵記錄；
// Clone 的複本與原本不共用輸入順序。
func TestComposerEditsForDynamicLayouts(t *testing.T) {
	var c Composer
	c.Add('ㄓ')
	c.Add('ㄧ')
	c.SetKeys("je")
	d := c.Clone()
	d.Replace(Initial, 'ㄐ')
	if d.String() != "ㄐㄧ" || c.String() != "ㄓㄧ" || d.Keys() != "" || c.Keys() != "je" {
		t.Fatalf("Replace：%q %q keys=%q %q", d.String(), c.String(), d.Keys(), c.Keys())
	}
	d.Backspace()
	if d.String() != "ㄐ" || c.String() != "ㄓㄧ" {
		t.Errorf("退格後應剩 ㄐ，原本不受影響：%q %q", d.String(), c.String())
	}
	e := c.Clone()
	e.Remove(Initial)
	e.Add('ㄢ')
	if e.String() != "ㄧㄢ" || c.String() != "ㄓㄧ" {
		t.Errorf("Remove：%q %q", e.String(), c.String())
	}
	e.Backspace()
	if e.String() != "ㄧ" {
		t.Errorf("Remove 後的輸入順序：%q", e.String())
	}
	e.Replace(Final, 'ㄣ') // 沒有韻母：不動作
	if e.String() != "ㄧ" {
		t.Errorf("Replace 不該補上沒有的符號：%q", e.String())
	}
}
