package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #273：「中英文之間自動加空格」預設關閉，舊設定檔沒有欄位時也是關；可以開、關，讀不懂的值退回預設（關）並在存檔時保留原文。
func TestCJKLatinSpaceDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.CJKLatinSpace {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("cjk-latin-space"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.CJKLatinSpace || s.ReadOnly() {
		t.Fatalf("舊設定檔沒有欄位時應是關、可寫：%+v %v", s, err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("cjk-latin-space", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.CJKLatinSpace != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"cjk_latin_space": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"cjk_latin_space":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.CJKLatinSpace {
		t.Fatalf("讀不懂的值退回預設：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"cjk_latin_space": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("cjk-latin-space", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, it := range Default().Items() {
		if it.Key == "cjk-latin-space" {
			if it.Value != "off" || it.Default != "off" || it.Label != "中英文之間自動加空格" || it.Locked ||
				!strings.Contains(it.Description, "「用Python寫」送出「用 Python 寫」") || !strings.Contains(it.Description, "數字旁不補") {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到中英文之間自動加空格的開關")
}

// 不屬於輸入習慣風格：選用內建風格時照樣可以單獨開關，換風格不改這個開關。
func TestCJKLatinSpaceIsNotLockedByProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, id := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
		s := freshSettings()
		if err := s.ProfileAction("select", id, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("cjk-latin-space", "on"); err != nil {
			t.Fatalf("%s 風格應可開啟：%v", id, err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.ReadOnly() || !back.CJKLatinSpace || back.ActiveProfile().ID != id {
			t.Fatalf("%s：重新讀取 %+v %v", id, back.ActiveProfile(), err)
		}
		if err := back.ProfileAction("select", "0c", ""); err != nil || !back.CJKLatinSpace {
			t.Fatalf("%s：換風格不改這個開關：%v", id, err)
		}
	}
}
