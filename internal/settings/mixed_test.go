package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedInputTogglePersistsAndDefaultsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.MixedInput {
		t.Fatalf("default: %+v %v", s, err)
	}
	if err := os.WriteFile(path, []byte(`{"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("mixed-input", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.MixedInput != (value == "on") || back.Selection != Statistical {
			t.Fatalf("restart: %+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("lost unknown setting: %s", b)
		}
	}
}

func TestMixedUnknownValueRetainedUntilExplicitChange(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"mixed_input":"later","punctuation":false}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.MixedInput || s.Punctuation {
		t.Fatal("invalid value must default only its own field")
	}
	s.Set("selection", Statistical)
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"mixed_input":"later"`) {
		t.Fatal(string(b))
	}
	s.Set("mixed-input", "off")
	b, _ = json.Marshal(s)
	if !strings.Contains(string(b), `"mixed_input":false`) {
		t.Fatal(string(b))
	}
}

// #147：中英混打開啟時單按 Shift 一樣切到英文模式；說明不能讓人以為開啟後就不能手動切換。
// 切換鍵可設成左、右或兩側（#117），英文補字的說明也不能只寫右 Shift。
func TestMixedInputDescriptionKeepsShiftToggle(t *testing.T) {
	items := map[string]Item{}
	for _, it := range Default().Items() {
		items[it.Key] = it
	}
	mixed := items["mixed-input"].Description
	if strings.Contains(mixed, "關閉後使用手動中英切換") || !strings.Contains(mixed, "Shift") {
		t.Fatalf("中英混打的說明要寫明開啟時仍可用 Shift 切換：%q", mixed)
	}
	if auto := items["english-autocomplete"].Description; strings.Contains(auto, "右 Shift") {
		t.Fatalf("切換鍵不一定是右 Shift：%q", auto)
	}
}
