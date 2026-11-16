package settings

import (
	"fmt"
	"testing"
)

// #288：中英混打開著時，注音打完按空白一律插入空白、↓ 選字（#78），「空白鍵選字」開或關都沒作用。
// 設定程式要把這一項畫成停用、寫出原因，並指向「中英混打」（在另一頁）；設定檔的值不動，混打實際不起作用時照舊。
func TestSpaceSelectsDisabledWhileMixedInputIsActive(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mixed, toneless bool
		layout, width   string
		disabled        bool
	}{
		{"預設", false, false, LayoutStandard, "off", false},
		{"混打", true, false, LayoutStandard, "off", true},
		{"混打＋所有符號鍵打半形（聲調選填暫停，混打照常）", true, true, LayoutStandard, "all", true},
		{"混打＋不打聲調（混打停用）", true, true, LayoutStandard, "off", false},
		{"混打＋倚天 41 鍵（混打停用）", true, false, LayoutETen, "off", false},
		{"混打＋許氏（混打停用）", true, false, LayoutHsu, "off", false},
		{"只開不打聲調", false, true, LayoutStandard, "off", false},
	} {
		for _, spaceSelects := range []bool{true, false} {
			for _, goos := range []string{"linux", "darwin", "windows"} {
				s := Default()
				s.MixedInput, s.Toneless, s.KeyboardLayout, s.SpaceSelects = tc.mixed, tc.toneless, tc.layout, spaceSelects
				s.ShiftSymbolHalfwidth, s.AllSymbolsHalfwidth = tc.width != "off", tc.width == "all"
				name := fmt.Sprintf("%s／空白鍵選字 %v／%s", tc.name, spaceSelects, goos)
				// 引擎的判斷（core/session 的 mixedInput）：混打開、標準排列、實際沒開聲調選填。
				if active := s.MixedInput && s.KeyboardLayout == LayoutStandard && !s.EffectiveToneless(); active != tc.disabled {
					t.Fatalf("%s：表格跟引擎的混打判斷不一致", name)
				}
				reason := s.disabledReasonOn(goos, "space-selects")
				if (reason != "") != tc.disabled {
					t.Fatalf("%s：停用=%v，原因 %q", name, tc.disabled, reason)
				}
				if tc.disabled && (reason != mixedSpaceReason || disabledBy(reason) != "mixed-input") {
					t.Fatalf("%s：原因 %q 指向 %q", name, reason, disabledBy(reason))
				}
			}
		}
	}

	s := Default()
	s.MixedInput, s.SpaceSelects = true, true
	it := itemOf(t, s, "space-selects")
	// 不帶 effective：引擎 health 回報的是設定檔的值，帶了會跟 running 比成「待套用」；停用的開關設定程式本來就畫成關閉。
	if !it.Disabled || it.DisabledReason != mixedSpaceReason || it.DisabledBy != "mixed-input" || it.Value != "on" || it.Effective != "" {
		t.Fatalf("混打開著時的空白鍵選字：%+v", it)
	}
	// 停用期間照樣能改，值保留到混打關掉。
	if err := s.Set("space-selects", "off"); err != nil || s.SpaceSelects {
		t.Fatalf("停用期間仍可改：%v %+v", err, s.SpaceSelects)
	}
	if err := s.Set("mixed-input", "off"); err != nil {
		t.Fatal(err)
	}
	if it := itemOf(t, s, "space-selects"); it.Disabled || it.DisabledReason != "" || it.DisabledBy != "" || it.Value != "off" {
		t.Fatalf("關掉混打後恢復：%+v", it)
	}
}

func itemOf(t *testing.T, s Settings, key string) Item {
	t.Helper()
	for _, it := range s.Items() {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("沒有 %s", key)
	return Item{}
}
