package main

import (
	"strings"
	"testing"
)

func list(t *testing.T, origin string) nameList {
	for _, l := range nameLists {
		if l.origin == origin {
			return l
		}
	}
	t.Fatalf("沒有 %s", origin)
	return nameList{}
}

func TestNameListForms(t *testing.T) {
	for _, c := range []struct{ origin, in, want string }{
		{"moe-schools", "市立崇明國中", "市立崇明國中,崇明國中"},
		{"moe-schools", "縣立敬恆國中(小)", "縣立敬恆國中,敬恆國中"},
		{"nhi-hospitals", "長庚醫療財團法人高雄長庚紀念醫院", "高雄長庚紀念醫院"},
		{"nhi-hospitals", "財團法人私立高雄醫學大學附設中和紀念醫院", "高雄醫學大學附設中和紀念醫院,中和紀念醫院"},
		{"moi-villages", "頂福里", "頂福里"},
	} {
		if got := strings.Join(list(t, c.origin).forms(c.in), ","); got != c.want {
			t.Errorf("%s %s → %s，應為 %s", c.origin, c.in, got, c.want)
		}
	}
}

func TestAgencyFilter(t *testing.T) {
	keep := list(t, "gov-agencies").keep
	for name, want := range map[string]bool{
		"法務部廉政署":       true,
		"阿蓮區農會":        true,
		"高雄市田寮區公所":     true,
		"上昕工程顧問股份有限公司": false,
		"兆基土木包工業":      false, // 不是機關字尾
	} {
		if got := keep(map[string]string{"機關名稱": name, "機關代碼": "A1", "單位代碼": ""}); got != want {
			t.Errorf("%s：%v，應為 %v", name, got, want)
		}
	}
	if keep(map[string]string{"機關名稱": "法務部廉政署", "機關代碼": "A1", "單位代碼": "B2"}) {
		t.Error("內部單位不應收")
	}
}
