package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #118：「輸出簡體字」預設關閉，舊設定檔沒有欄位時不變；開啟後可保存、改回，
// 讀不懂的值退回關閉並在存檔時保留原文。
func TestOutputSimplifiedDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.OutputSimplified {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("output-simplified"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.OutputSimplified {
		t.Fatalf("舊設定檔：%+v %v", s, err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("output-simplified", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.OutputSimplified != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"output_simplified": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"output_simplified":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.OutputSimplified {
		t.Fatalf("讀不懂的值退回關閉：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"output_simplified": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	for _, it := range Default().Items() {
		if it.Key == "output-simplified" {
			if it.Value != "off" || it.Default != "off" || it.Label != "輸出簡體字" || !strings.Contains(it.Description, "仍顯示繁體") {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到輸出簡體字的開關")
}
