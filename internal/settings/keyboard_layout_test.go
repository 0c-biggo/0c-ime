package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #83：鍵盤排列預設標準；舊設定檔沒有欄位時不變；選倚天 41 鍵、許氏（#183）可保存、改回。
func TestKeyboardLayoutDefaultsStandardAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.KeyboardLayout != LayoutStandard {
		t.Fatalf("預設標準排列：%+v %v", s, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"mixed_input":true,"future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.KeyboardLayout != LayoutStandard {
		t.Fatalf("舊設定檔：%+v %v", s, err)
	}
	for _, value := range []string{"eten", "standard", "hsu", "eten"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("keyboard-layout", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.KeyboardLayout != value || !back.MixedInput {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"keyboard_layout": "`+value+`"`) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	var s Settings
	if err := s.Set("keyboard-layout", "eten26"); err == nil {
		t.Fatal("不認得的排列應拒絕")
	}
}

// 較新版本寫入本版不認得的排列：退回標準排列，存檔時原值保留。
func TestKeyboardLayoutUnknownValueKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"schema":1,"keyboard_layout":"eten26"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.KeyboardLayout != LayoutStandard {
		t.Fatalf("讀不懂的排列用標準：%+v %v", s, err)
	}
	if err := s.Set("esc-clear", "off"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"keyboard_layout":"eten26"`) && !strings.Contains(string(b), `"keyboard_layout": "eten26"`) {
		t.Fatalf("原值應保留：%s", b)
	}
}

// 倚天 41 鍵時，中英混打與 Emoji 選單（含子選項）標為停用並附原因；值保留，切回標準排列就照舊可改。
func TestETenDisablesMixedAndEmojiItems(t *testing.T) {
	s := Default()
	s.MixedInput = true
	s.Set("toggle-key", ToggleRight) // macOS 預設不用 Shift，開始模式另外停用（#184）；這裡只看倚天
	disabled := func(s Settings) map[string]string {
		out := map[string]string{}
		for _, it := range s.Items() {
			if it.Disabled {
				out[it.Key] = it.DisabledReason
			}
		}
		return out
	}
	if d := disabled(s); len(d) != 0 {
		t.Fatalf("標準排列沒有停用的項目：%v", d)
	}
	if err := s.Set("keyboard-layout", "eten"); err != nil {
		t.Fatal(err)
	}
	d := disabled(s)
	// 不打聲調（#150）只在標準排列有效，倚天時也停用。
	if len(d) != 4 || d["mixed-input"] != "倚天 41 鍵時關閉" || d["emoji-shortcut"] != "倚天 41 鍵時關閉" || d["emoji-equals-first"] == "" || d["toneless"] != "倚天 41 鍵時關閉" {
		t.Fatalf("倚天停用混打與 Emoji：%v", d)
	}
	for _, it := range s.Items() {
		if it.Key == "mixed-input" && it.Value != "on" || it.Key == "emoji-shortcut" && it.Value != "equals" {
			t.Fatalf("停用時仍回報設定檔中的值：%+v", it)
		}
	}
	if err := s.Set("keyboard-layout", "standard"); err != nil {
		t.Fatal(err)
	}
	if d := disabled(s); len(d) != 0 || !s.MixedInput || s.EmojiShortcut != "equals" {
		t.Fatalf("切回標準排列恢復原本的值：%v %+v", d, s)
	}
}

// #183：許氏只停用中英混打（聲調在字母鍵上）與只限標準排列的不打聲調（#150）；= 不是注音鍵，Emoji 選單照舊。值保留，切回標準排列照舊。
func TestHsuDisablesOnlyMixedInput(t *testing.T) {
	s := Default()
	s.MixedInput = true
	s.Set("toggle-key", ToggleRight)
	if err := s.Set("keyboard-layout", "hsu"); err != nil {
		t.Fatal(err)
	}
	var disabled []string
	for _, it := range s.Items() {
		if it.Disabled {
			disabled = append(disabled, it.Key+"："+it.DisabledReason)
		}
		if it.Key == "mixed-input" && it.Value != "on" {
			t.Fatalf("停用時仍回報設定檔中的值：%+v", it)
		}
		if it.Key == "keyboard-layout" && it.ValueLabels["hsu"] != "許氏" {
			t.Fatalf("排列的顯示名稱：%+v", it.ValueLabels)
		}
	}
	// 不打聲調（#150）只在標準排列有效，許氏時也停用。
	if strings.Join(disabled, "、") != "mixed-input：許氏排列時關閉、toneless：許氏排列時關閉" {
		t.Fatalf("許氏只停用混打：%v", disabled)
	}
}
