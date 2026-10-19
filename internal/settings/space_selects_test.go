package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #107、#110：「空白鍵選字」預設開啟，舊設定檔沒有欄位時不變；關閉後可保存、改回。
func TestSpaceSelectsDefaultsOnAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || !s.SpaceSelects {
		t.Fatalf("預設開啟：空白鍵選字，舊使用者行為不變：%+v %v", s, err)
	}
	if v, _ := Default().Value("space-selects"); v != "on" {
		t.Fatalf("命令列預設值：%q", v)
	}
	// 舊設定檔沒有這個欄位：維持預設開啟。
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || !s.SpaceSelects {
		t.Fatalf("舊設定檔：%+v %v", s, err)
	}
	for _, value := range []string{"off", "on", "off"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("space-selects", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.SpaceSelects != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"space_selects": `+map[string]string{"on": "true", "off": "false"}[value]) {
			t.Fatalf("settings.json 沒有 space_selects：%s", b)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("未知欄位遺失：%s", b)
		}
	}
}
