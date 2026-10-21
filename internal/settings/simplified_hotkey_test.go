package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #192：「用快捷鍵切換簡體字」預設關閉，舊設定檔沒有欄位時也是關；可以開、改回，讀不懂的值退回預設並保留原文。
// 設定程式把它排在「輸出簡體字」後面，說明寫出這個平台的快捷鍵。
func TestSimplifiedHotkeyDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.SimplifiedHotkey {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("simplified-hotkey"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"output_simplified":true,"future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil || (value == "on") == s.SimplifiedHotkey {
			t.Fatalf("改之前：%+v %v", s, err)
		}
		if err := s.Set("simplified-hotkey", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.SimplifiedHotkey != (value == "on") || !back.OutputSimplified {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"simplified_hotkey": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"simplified_hotkey":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.SimplifiedHotkey {
		t.Fatalf("讀不懂的值退回預設：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"simplified_hotkey": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("simplified-hotkey", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	items := Default().Items()
	for i, it := range items {
		if it.Key == "simplified-hotkey" {
			if it.Value != "off" || it.Default != "off" || items[i-1].Key != "output-simplified" ||
				!strings.Contains(it.Description, SimplifiedHotkeyLabel(runtime.GOOS)) {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到快捷鍵的開關")
}

// 說明寫出這個平台的鍵：Mac 用 ⌃⇧ 符號並提醒 fn，Windows／Linux 用 Ctrl＋Shift。
func TestSimplifiedHotkeyDescriptionNamesThePlatformKey(t *testing.T) {
	for goos, want := range map[string]string{"windows": "Ctrl＋Shift＋", "linux": "Ctrl＋Shift＋", "darwin": "⌃⇧"} {
		d := simplifiedHotkeyDescription(goos)
		if !strings.Contains(d, want) || !strings.Contains(d, SimplifiedHotkeyLabel(goos)) || strings.Contains(d, "fn") != (goos == "darwin") {
			t.Fatalf("%s：%s", goos, d)
		}
	}
}
