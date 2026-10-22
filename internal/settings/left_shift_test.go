package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLeftShiftDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	// 預設跟著平台的中英切換鍵：Windows 左右 Shift（#206）時開，其他平台關。
	on := defaultToggleKey(runtime.GOOS) == ToggleBoth
	if s, err := Load(path); err != nil || s.LeftShift != on {
		t.Fatalf("預設：%+v %v", s, err)
	}
	if v, _ := Default().Value("left-shift"); v != onOff(on) {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("left-shift", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.LeftShift != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("未知欄位遺失：%s", b)
		}
	}
}

func TestLeftShiftUnknownValueRetainedUntilExplicitChange(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"left_shift":"both","punctuation":true}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.LeftShift != Default().LeftShift || !s.Punctuation {
		t.Fatal("讀不懂的值只讓這一欄退回預設")
	}
	s.Set("selection", Statistical)
	if b, _ := json.Marshal(s); !strings.Contains(string(b), `"left_shift":"both"`) {
		t.Fatal(string(b))
	}
	s.Set("left-shift", "off")
	if b, _ := json.Marshal(s); !strings.Contains(string(b), `"left_shift":false`) {
		t.Fatal(string(b))
	}
}
