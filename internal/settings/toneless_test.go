package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #150：「不打聲調也能選字」預設關閉，舊設定檔沒有欄位時也是關；可以開、改回，讀不懂的值退回預設並保留原文。
// 開著時設定程式的中英混打反灰（與混打二選一），已儲存的 mixed_input 不動。
func TestTonelessDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.Toneless {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("toneless"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"mixed_input":true,"future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil || (value == "on") == s.Toneless {
			t.Fatalf("改之前：%+v %v", s, err)
		}
		if err := s.Set("toneless", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.Toneless != (value == "on") || !back.MixedInput {
			t.Fatalf("重新讀取（混打的值不動）：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"toneless": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
		var mixed Item
		for _, it := range back.Items() {
			if it.Key == "mixed-input" {
				mixed = it
			}
		}
		if mixed.Disabled != (value == "on") || (value == "on") != (mixed.DisabledReason == "不打聲調時關閉") {
			t.Fatalf("不打聲調時混打反灰：%+v", mixed)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"toneless":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.Toneless {
		t.Fatalf("讀不懂的值退回預設：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"toneless": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("toneless", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, it := range Default().Items() {
		if it.Key == "toneless" {
			if it.Value != "off" || it.Default != "off" || !strings.Contains(it.Description, "Enter") || !strings.Contains(it.Description, "混打") {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到不打聲調的開關")
}

// #207：不打聲調時反灰的中英混打指向「不打聲調也能選字」，設定程式據此連到「選字」頁；排列造成的停用照舊指向鍵盤排列。
func TestTonelessDisabledMixedInputNamesItsCause(t *testing.T) {
	s := Default()
	s.Toneless, s.MixedInput = true, true
	for _, it := range s.Items() {
		if it.Key == "mixed-input" && (it.DisabledBy != "toneless" || it.DisabledReason != "不打聲調時關閉") {
			t.Fatalf("混打指向不打聲調：%+v", it)
		}
	}
	s.KeyboardLayout = LayoutHsu
	for _, it := range s.Items() {
		if (it.Key == "mixed-input" || it.Key == "toneless") && it.DisabledBy != "keyboard-layout" {
			t.Fatalf("許氏時指向鍵盤排列：%+v", it)
		}
	}
}

// 不打聲調只在標準排列有效：倚天、許氏時反灰並說明原因，值保留；混打的原因以排列為準。
func TestTonelessDisabledOutsideStandardLayout(t *testing.T) {
	for layout, reason := range map[string]string{LayoutETen: "倚天 41 鍵時關閉", LayoutHsu: "許氏排列時關閉", LayoutStandard: ""} {
		s := Default()
		s.Toneless, s.MixedInput = true, true
		if err := s.Set("keyboard-layout", layout); err != nil {
			t.Fatal(err)
		}
		for _, it := range s.Items() {
			if it.Key == "toneless" && (it.DisabledReason != reason || it.Disabled != (reason != "") || it.Value != "on") {
				t.Fatalf("%s：%+v", layout, it)
			}
			if it.Key == "mixed-input" && reason != "" && it.DisabledReason != reason {
				t.Fatalf("%s 混打的原因以排列為準：%+v", layout, it)
			}
		}
	}
}
