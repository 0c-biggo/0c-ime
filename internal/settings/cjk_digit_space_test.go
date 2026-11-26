package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 「中文與數字之間自動加空格」預設關閉，舊設定檔沒有欄位時也是關；可以開、關，跟中英文空格互不影響。
func TestCJKDigitSpaceDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.CJKDigitSpace {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("cjk-digit-space"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("cjk-digit-space", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.CJKDigitSpace != (value == "on") || back.CJKLatinSpace {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"cjk_digit_space": `+map[string]string{"on": "true", "off": "false"}[value]) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if d := Default(); d.Set("cjk-digit-space", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, it := range Default().Items() {
		if it.Key == "cjk-digit-space" {
			if it.Value != "off" || it.Default != "off" || it.Label != "中文與數字之間自動加空格" || it.Locked {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到中文與數字之間自動加空格的開關")
}
