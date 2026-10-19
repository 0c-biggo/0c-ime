package settings

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// #160：開始時的中英模式。舊檔沒有這個欄位時是注音（跟以前一樣）；讀不懂的值退回注音、存檔時原樣寫回。
func TestStartModeDefaultAndCompat(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"mode_memory":"native"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.StartMode != StartZhuyin || s.StartsEnglish() {
		t.Fatalf("舊檔沒有欄位：%+v", s)
	}
	if Default().StartMode != StartZhuyin {
		t.Fatal("預設注音")
	}
	json.Unmarshal([]byte(`{"start_mode":"english"}`), &s)
	if !s.StartsEnglish() {
		t.Fatal("english")
	}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"start_mode":"english"`) {
		t.Fatalf("寫回：%s", b)
	}
	json.Unmarshal([]byte(`{"start_mode":"latin"}`), &s)
	if s.StartsEnglish() {
		t.Fatal("讀不懂的值退回注音")
	}
	if b, _ = json.Marshal(s); !strings.Contains(string(b), `"start_mode":"latin"`) {
		t.Fatalf("讀不懂的值原樣寫回：%s", b)
	}
	if notes := strings.Join(s.notes(), "\n"); !strings.Contains(notes, "start-mode") {
		t.Fatalf("說明退回：%s", notes)
	}
	if err := s.Set("start-mode", "english"); err != nil || !s.StartsEnglish() {
		t.Fatal(err)
	}
	if b, _ = json.Marshal(s); strings.Contains(string(b), "latin") {
		t.Fatalf("明確設定後不再寫回舊值：%s", b)
	}
	if err := s.Set("start-mode", "chinese"); err == nil {
		t.Fatal("不認得的值")
	}
	if v, ok := s.Value("start-mode"); !ok || v != StartEnglish {
		t.Fatal(v)
	}
}

// 設定程式：放在中英切換鍵與切換視窗後的中英模式之間；mode-memory 的 native 在 macOS／Linux 是
// 「每次回到…」，名稱跟著開始時的模式。
func TestStartModeItem(t *testing.T) {
	s := Default()
	items := s.Items()
	index := map[string]int{}
	for i, item := range items {
		index[item.Key] = i
	}
	i, ok := index["start-mode"]
	if !ok || index["toggle-key"] != i-1 || index["mode-memory"] != i+1 {
		t.Fatalf("位置：%v", index)
	}
	item := items[i]
	if item.Label != "開始時的中英模式" || item.Default != StartZhuyin || item.Value != StartZhuyin ||
		item.ValueLabels[StartZhuyin] != "注音" || item.ValueLabels[StartEnglish] != "英文" {
		t.Fatalf("%+v", item)
	}
	if _, ok := Keys()["start-mode"]; !ok {
		t.Fatal("Keys")
	}
	if modeNativeLabel("linux", true) != "每次回到英文" || modeNativeLabel("darwin", false) != "每次回到注音" ||
		modeNativeLabel("windows", true) != "每個程式各自記住" {
		t.Fatal("native 的名稱")
	}
	s.Set("start-mode", "english")
	for _, item := range s.Items() {
		if item.Key == "mode-memory" && item.ValueLabels[ModeNative] != modeNativeLabel(runtime.GOOS, true) {
			t.Fatalf("%+v", item)
		}
	}
	if !strings.Contains(startModeDescription("windows"), "新開的程式") || !strings.Contains(startModeDescription("linux"), "輸入框") {
		t.Fatal("說明依平台")
	}
}
