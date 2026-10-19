package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscClearDefaultsOnAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || !s.EscClear {
		t.Fatalf("預設要開啟（多數注音輸入法的預設）：%+v %v", s, err)
	}
	if v, _ := Default().Value("esc-clear"); v != "on" {
		t.Fatalf("命令列預設值：%q", v)
	}
	// 舊設定檔沒有這個欄位：維持預設開啟。
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || !s.EscClear {
		t.Fatalf("舊設定檔：%+v %v", s, err)
	}
	for _, value := range []string{"off", "on", "off"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("esc-clear", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.EscClear != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("未知欄位遺失：%s", b)
		}
	}
}
