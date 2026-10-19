package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

// #117：舊檔 left_shift 推出切換鍵；新檔以 toggle_key 為準，存檔時一併寫回 left_shift 給舊版讀。
func TestToggleKeyMigrationAndCompat(t *testing.T) {
	var s Settings
	json.Unmarshal([]byte(`{"left_shift":true}`), &s)
	if s.ToggleKey != ToggleBoth || !s.TogglesLeft() || !s.TogglesRight() {
		t.Fatalf("舊檔：%+v", s)
	}
	json.Unmarshal([]byte(`{}`), &s)
	if s.ToggleKey != ToggleRight || s.LeftShift {
		t.Fatalf("預設右 Shift：%+v", s)
	}
	json.Unmarshal([]byte(`{"toggle_key":"left","left_shift":false}`), &s)
	if s.ToggleKey != ToggleLeft || !s.LeftShift || s.TogglesRight() {
		t.Fatalf("新欄位為準：%+v", s)
	}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"left_shift":true`) || !strings.Contains(string(b), `"toggle_key":"left"`) {
		t.Fatalf("寫回舊欄位：%s", b)
	}
	// 舊鍵 left-shift 只改左 Shift。
	s = Default()
	s.Set("left-shift", "on")
	if s.ToggleKey != ToggleBoth {
		t.Fatal(s.ToggleKey)
	}
	s.Set("toggle-key", "none")
	s.Set("left-shift", "on")
	if s.ToggleKey != ToggleLeft {
		t.Fatal(s.ToggleKey)
	}
	if err := s.Set("toggle-key", "ctrl"); err == nil {
		t.Fatal("不認得的值")
	}
}

func TestModeMemoryDefaultsByPlatform(t *testing.T) {
	if defaultModeMemory("darwin") != ModeShared || defaultModeMemory("windows") != ModeNative || defaultModeMemory("linux") != ModeNative {
		t.Fatal("macOS 全部共用，其他照平台原本的做法")
	}
	if modeNativeLabel("windows", false) != "每個程式各自記住" || modeNativeLabel("linux", false) != "每次回到注音" {
		t.Fatal()
	}
}

// #117：Ctrl＋空白鍵是 Windows 繁中輸入法的系統熱鍵，只在 Windows 的說明裡提。
func TestToggleKeyDescriptionMentionsCtrlSpaceOnWindows(t *testing.T) {
	if !strings.HasSuffix(toggleKeyDescription("windows"), "也可以按 Ctrl＋空白鍵") {
		t.Fatal(toggleKeyDescription("windows"))
	}
	for _, goos := range []string{"linux", "darwin"} {
		if strings.Contains(toggleKeyDescription(goos), "Ctrl") {
			t.Fatal(goos, toggleKeyDescription(goos))
		}
	}
}
