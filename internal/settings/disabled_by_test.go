package settings

import "testing"

// 設定程式把各項分在不同頁面（#207）：停用的項目要說出是哪一項造成的，才能帶人到那一頁去改。
// 新增停用原因卻忘了在 disabledBy 登記時，這裡會失敗。
func TestDisabledItemsNameTheirCause(t *testing.T) {
	keys := map[string]bool{}
	for _, sp := range specs() {
		keys[sp.key] = true
	}
	for _, layout := range []string{LayoutStandard, LayoutETen, LayoutHsu} {
		for _, toggle := range []string{ToggleRight, ToggleBoth, ToggleLeft, ToggleNone} {
			s := Default()
			s.KeyboardLayout, s.ToggleKey, s.MixedInput, s.StartMode = layout, toggle, true, StartEnglish
			for _, goos := range []string{"linux", "darwin", "windows"} {
				for key := range keys {
					reason := s.disabledReasonOn(goos, key)
					if reason == "" {
						continue
					}
					if by := disabledBy(reason); !keys[by] || by == key {
						t.Errorf("%s／%s／%s：%s 停用（%s）沒有指出原因所在的設定（%q）", layout, toggle, goos, key, reason, by)
					}
				}
			}
			for _, it := range s.Items() {
				if it.Disabled != (it.DisabledBy != "") {
					t.Errorf("%s／%s：%s 的 disabled=%v 但 disabled_by=%q", layout, toggle, it.Key, it.Disabled, it.DisabledBy)
				}
			}
		}
	}
	hsu := Default()
	hsu.KeyboardLayout = LayoutHsu
	for _, it := range hsu.Items() {
		if it.Key == "mixed-input" && it.DisabledBy != "keyboard-layout" {
			t.Fatalf("許氏時的中英混打要指向鍵盤排列：%+v", it)
		}
	}
	none := Default()
	none.ToggleKey = ToggleNone
	if by := disabledBy(none.disabledReasonOn("linux", "start-mode")); by != "toggle-key" {
		t.Fatalf("不用 Shift 時的開始模式要指向中英切換鍵：%q", by)
	}
}
