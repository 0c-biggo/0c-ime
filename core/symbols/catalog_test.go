package symbols

import (
	"slices"
	"testing"
	"unicode/utf8"
)

// 每個符號只列在一個分頁（↑ 才定位得到），ID 不重複；分頁名稱不與「最近」相同。
func TestTabsUniqueAndIDsStable(t *testing.T) {
	seen := map[string]string{}
	ids := map[int]string{}
	for _, tab := range Tabs {
		if tab.Name == Recent || len(tab.Items) == 0 {
			t.Fatalf("分頁 %q", tab.Name)
		}
		for i, item := range tab.Items {
			if other, ok := seen[item]; ok {
				t.Errorf("%q 同時在 %s 與 %s", item, other, tab.Name)
			}
			seen[item] = tab.Name
			if !utf8.ValidString(item) || item == "" {
				t.Errorf("%s 第 %d 項", tab.Name, i)
			}
			id := ID(item)
			if id < IDBase || ids[id] != "" {
				t.Errorf("%q 的 ID %d", item, id)
			}
			ids[id] = item
			if at, index, ok := Locate(item); !ok || Tabs[at].Name != tab.Name || index != i {
				t.Errorf("Locate(%q)", item)
			}
		}
	}
	if ID("任意文字") != -1 || Contains("任意文字") {
		t.Fatal("目錄外")
	}
}

func TestCleanRecent(t *testing.T) {
	in := []string{"※", "x", "※", "，"}
	out := CleanRecent(in)
	if !slices.Equal(out, []string{"※", "，"}) {
		t.Fatal(out)
	}
	out[0] = "改"
	if in[0] != "※" {
		t.Fatal("不與輸入共用")
	}
	var many []string
	for _, tab := range Tabs {
		many = append(many, tab.Items...)
	}
	if len(CleanRecent(many)) != MaxRecent {
		t.Fatal("有上限")
	}
}
