package settings

import (
	"encoding/json"
	"runtime"
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
	if s.ToggleKey != Default().ToggleKey || s.LeftShift != s.TogglesLeft() {
		t.Fatalf("缺漏欄位沿用平台預設：%+v", s)
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
	s.Set("toggle-key", "right")
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

func TestToggleKeyDefaultAndSavedChoices(t *testing.T) {
	want := defaultToggleKey(runtime.GOOS)
	for _, body := range []string{`{}`, `{"left_shift":false}`, `{"mixed_input":true}`} {
		var s Settings
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatal(err)
		}
		if s.ToggleKey != want || s.TogglesRight() != (want != ToggleNone) || s.TogglesLeft() != (want == ToggleBoth) {
			t.Fatalf("缺少切換鍵設定時使用平台預設：%s → %+v", body, s)
		}
	}
	for _, value := range []string{ToggleRight, ToggleBoth, ToggleLeft, ToggleNone} {
		var s Settings
		if err := json.Unmarshal([]byte(`{"toggle_key":"`+value+`"}`), &s); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("selection", Statistical); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var back Settings
		if err := json.Unmarshal(data, &back); err != nil || back.ToggleKey != value {
			t.Fatalf("更改其他設定後保留已儲存的切換鍵：%s → %+v %v", data, back, err)
		}
	}
	for _, item := range Default().Items() {
		if item.Key == "toggle-key" && (item.Default != want || item.Value != want) {
			t.Fatalf("設定程式的預設與目前值應一致：%+v", item)
		}
	}
}

// #206：Windows 比照系統內建注音，左右 Shift 都切換；macOS 不用 Shift（#167），Linux 維持右 Shift（Fcitx5 的左 Shift 是暫時切換輸入法）。
func TestToggleKeyDefaultsByPlatform(t *testing.T) {
	if defaultToggleKey("windows") != ToggleBoth || defaultToggleKey("darwin") != ToggleNone || defaultToggleKey("linux") != ToggleRight {
		t.Fatal(defaultToggleKey("windows"), defaultToggleKey("darwin"), defaultToggleKey("linux"))
	}
	if !strings.Contains(toggleKeyDescription("windows"), "預設左右 Shift 都可以切換，不想讓左 Shift 切換可改成「右 Shift」") {
		t.Fatal(toggleKeyDescription("windows"))
	}
	for _, goos := range []string{"linux", "darwin"} {
		if strings.Contains(toggleKeyDescription(goos), "左右") {
			t.Fatal(goos, toggleKeyDescription(goos))
		}
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
