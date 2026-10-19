package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #133：「選字後游標移到下一個字」預設關閉，舊設定檔沒有欄位時不變；開啟後可保存、改回，
// 讀不懂的值退回關閉並在存檔時保留原文。
func TestAdvanceAfterSelectDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.AdvanceAfterSelect {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("advance-after-select"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.AdvanceAfterSelect {
		t.Fatalf("舊設定檔：%+v %v", s, err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("advance-after-select", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.AdvanceAfterSelect != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"advance_after_select": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"advance_after_select":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.AdvanceAfterSelect {
		t.Fatalf("讀不懂的值退回關閉：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"advance_after_select": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
}
