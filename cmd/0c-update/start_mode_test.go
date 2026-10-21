package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// #160：引擎回報的開始時中英模式對回設定的寫法，設定程式才知道改了之後是否已套用；舊引擎沒有回報時不出現。
func TestRunningValuesStartMode(t *testing.T) {
	if v, ok := runningValues(map[string]string{"start_mode": "english"})["start-mode"]; !ok || v != "english" {
		t.Fatal(v)
	}
	if _, ok := runningValues(map[string]string{"version": "old"})["start-mode"]; ok {
		t.Fatal("舊引擎沒有回報")
	}
}

// #112：引擎回報的選字鍵對回設定的寫法；舊引擎沒有回報時不出現。
func TestRunningValuesSelectionKeys(t *testing.T) {
	if v, ok := runningValues(map[string]string{"selection_keys": "1234qweras"})["selection-keys"]; !ok || v != "1234qweras" {
		t.Fatal(v)
	}
	if _, ok := runningValues(map[string]string{"version": "old"})["selection-keys"]; ok {
		t.Fatal("舊引擎沒有回報")
	}
}

// settings 的文字清單要列出設定程式看得到的每一項（曾經漏過 toggle-key、mode-memory）。
func TestPrintSettingsListsEveryItem(t *testing.T) {
	s := settings.Default()
	s.Set("start-mode", "english")
	var out bytes.Buffer
	listSettings(&out, settings.Report{Path: "/tmp/settings.json", Settings: s}, "/tmp/settings.json")
	text := out.String()
	for _, item := range s.Items() {
		if !strings.Contains(text, "\n"+item.Key+"=") {
			t.Errorf("沒有列出 %s：\n%s", item.Key, text)
		}
	}
	if !strings.Contains(text, "\nstart-mode=english（") {
		t.Fatal(text)
	}
}

// #184：不用 Shift 時開始模式固定為注音。引擎回報 zhuyin（它真的從注音開始）時不算「尚未套用」；
// 設定檔仍是 english，介面靠 effective 畫成注音並停用。
func TestStartModeLockedWithoutShiftIsNotPending(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 有 Ctrl＋空白鍵，不固定")
	}
	base := t.TempDir()
	writeSettings(t, base, `{"toggle_key":"none","start_mode":"english"}`)
	for _, tc := range []struct {
		running string
		pending bool
	}{{"zhuyin", false}, {"english", true}} { // english：還是改設定前的舊引擎
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"toggle_key": "none", "start_mode": tc.running}, true))
		it := item(t, doc, "start-mode")
		if it.Value != "english" || it.Effective != "zhuyin" || !it.Disabled || it.Pending != tc.pending || doc.PendingRestart != tc.pending {
			t.Fatalf("running %s：%+v", tc.running, it)
		}
	}
	s := settings.Default()
	s.Set("toggle-key", "none")
	s.Set("start-mode", "english")
	var out bytes.Buffer
	listSettings(&out, settings.Report{Path: "/tmp/settings.json", Settings: s}, "/tmp/settings.json")
	if !strings.Contains(out.String(), "\nstart-mode=english（開始時的中英模式：zhuyin 注音；english 英文；目前不用 Shift 切換，固定從注音開始）") {
		t.Fatal(out.String())
	}
}
