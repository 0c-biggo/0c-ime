package main

import (
	"bytes"
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
