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
	json.Unmarshal([]byte(`{"toggle_key":"right","start_mode":"english"}`), &s) // 不用 Shift 時固定注音（#184），macOS 的預設就是不用
	if !s.StartsEnglish() {
		t.Fatal("english")
	}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"start_mode":"english"`) {
		t.Fatalf("寫回：%s", b)
	}
	json.Unmarshal([]byte(`{"toggle_key":"right","start_mode":"latin"}`), &s)
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
	s.Set("toggle-key", ToggleRight) // macOS 預設不用 Shift，開始模式會固定為注音（#184）
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

// #184：中英切換鍵設為不用 Shift 時，macOS 與 Linux 沒有別的方法從英文切回注音，開始時的模式固定為注音：
// 引擎從注音開始、回報給外殼的是注音，設定程式把這一項停用並說明原因、選項畫在注音；設定檔裡的英文保留，
// 改回用 Shift 切換就照原本的選擇。Windows 可以按 Ctrl＋空白鍵或點工作列的中／英，照設定。
func TestStartModeFixedToZhuyinWithoutShift(t *testing.T) {
	s := Default()
	s.Set("toggle-key", ToggleNone)
	if err := s.Set("start-mode", StartEnglish); err != nil {
		t.Fatal(err) // 0c-update set 不因停用而拒絕，值保留
	}
	for _, goos := range []string{"darwin", "linux"} {
		if s.startsEnglish(goos) || s.disabledReasonOn(goos, "start-mode") != startModeLockedReason {
			t.Fatalf("%s：不用 Shift 時要固定注音", goos)
		}
	}
	if !s.startsEnglish("windows") || s.disabledReasonOn("windows", "start-mode") != "" {
		t.Fatal("Windows 有 Ctrl＋空白鍵，照設定從英文開始")
	}
	for _, key := range []string{ToggleRight, ToggleBoth, ToggleLeft} {
		other := s
		other.Set("toggle-key", key)
		if !other.startsEnglish("darwin") || !other.startsEnglish("linux") || other.disabledReasonOn("darwin", "start-mode") != "" {
			t.Fatalf("%s：有 Shift 可以切換時照設定", key)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	if s.StartsEnglish() || s.EffectiveStartMode() != StartZhuyin {
		t.Fatal("引擎與回報給外殼的開始模式")
	}
	if v, _ := s.Value("start-mode"); v != StartEnglish || s.StartMode != StartEnglish {
		t.Fatal("設定檔的值不動")
	}
	item := func(s Settings, key string) Item {
		for _, it := range s.Items() {
			if it.Key == key {
				return it
			}
		}
		t.Fatal(key)
		return Item{}
	}
	if it := item(s, "start-mode"); !it.Disabled || it.DisabledReason != startModeLockedReason || it.Value != StartEnglish || it.Effective != StartZhuyin {
		t.Fatalf("停用並畫成注音：%+v", it)
	}
	if it := item(s, "mode-memory"); it.ValueLabels[ModeNative] != modeNativeLabel(runtime.GOOS, false) {
		t.Fatalf("切換視窗後的名稱照實際的開始模式：%+v", it)
	}
	zh := s
	zh.Set("start-mode", StartZhuyin)
	if it := item(zh, "start-mode"); !it.Disabled || it.Effective != "" {
		t.Fatalf("已經是注音時不另給 effective：%+v", it)
	}
	s.Set("toggle-key", ToggleRight)
	if it := item(s, "start-mode"); it.Disabled || it.Effective != "" || !s.StartsEnglish() || s.EffectiveStartMode() != StartEnglish {
		t.Fatalf("改回用 Shift 照原本的英文：%+v", it)
	}
}
