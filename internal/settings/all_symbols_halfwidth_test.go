package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #282：「標點寬度」三選一（shift-symbol-halfwidth：off／on／all）。all 存成 shift_symbol_halfwidth＋all_symbols_halfwidth
// 兩個 true：舊版只認得前者，讀到最接近的「Shift＋符號打半形」；舊版關掉前者時，新版不會因後者的殘值變回全部半形。
func TestAllSymbolsHalfwidthPersistsAndStaysCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"schema":2,"selection":"statistical","shift_symbol_halfwidth":true,"future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if v, _ := s.Value("shift-symbol-halfwidth"); err != nil || v != "on" {
		t.Fatalf("舊設定檔的 true 是 Shift＋符號打半形：%q %v", v, err)
	}
	for _, value := range []string{"all", "on", "off", "all"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("shift-symbol-halfwidth", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if v, _ := back.Value("shift-symbol-halfwidth"); err != nil || v != value || back.Selection != Statistical || back != s {
			t.Fatalf("%s 重新讀取：%q %v", value, v, err)
		}
		b, _ := os.ReadFile(path)
		want := map[string][2]string{"off": {"false", "false"}, "on": {"true", "false"}, "all": {"true", "true"}}[value]
		if !strings.Contains(string(b), `"shift_symbol_halfwidth": `+want[0]) || !strings.Contains(string(b), `"all_symbols_halfwidth": `+want[1]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("%s 的 settings.json：%s", value, b)
		}
	}
	// 舊版把 shift_symbol_halfwidth 關掉、留下 all_symbols_halfwidth 的殘值：照關閉。
	if err := os.WriteFile(path, []byte(`{"schema":2,"shift_symbol_halfwidth":false,"all_symbols_halfwidth":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.AllSymbolsHalfwidth || s.ShiftSymbolHalfwidth {
		t.Fatalf("殘值不應打開全部半形：%+v %v", s, err)
	}
}

// 「所有符號鍵打半形」時 Ctrl＋標點固定開啟、聲調選填暫停（設定檔的值不動），停用原因帶到「標點寬度」；換回來照舊。
func TestAllSymbolsHalfwidthFixesCtrlAndPausesToneless(t *testing.T) {
	s := Default()
	for _, kv := range [][2]string{{"ctrl-punctuation", "off"}, {"toneless", "on"}, {"shift-symbol-halfwidth", "all"}} {
		if err := s.Set(kv[0], kv[1]); err != nil {
			t.Fatalf("%v：%v", kv, err)
		}
	}
	if !s.EffectiveCtrlPunctuation() || s.EffectiveToneless() || s.CtrlPunctuation || !s.Toneless {
		t.Fatalf("實際值：ctrl=%v toneless=%v，設定檔 ctrl=%v toneless=%v", s.EffectiveCtrlPunctuation(), s.EffectiveToneless(), s.CtrlPunctuation, s.Toneless)
	}
	items := map[string]Item{}
	for _, it := range s.Items() {
		items[it.Key] = it
	}
	if it := items["ctrl-punctuation"]; !it.Disabled || it.DisabledBy != "shift-symbol-halfwidth" || it.Effective != "on" || it.Value != "off" {
		t.Fatalf("Ctrl＋標點：%+v", it)
	}
	if it := items["toneless"]; !it.Disabled || it.DisabledBy != "shift-symbol-halfwidth" || it.Effective != "off" || it.Value != "on" {
		t.Fatalf("聲調選填：%+v", it)
	}
	if it := items["mixed-input"]; it.Disabled {
		t.Fatalf("聲調選填暫停時中英混打可用：%+v", it)
	}
	// 設定檔本來就開著 Ctrl＋標點時也帶 effective：設定程式把停用的開關畫成關閉，沒有 effective 會畫錯。
	d := Default()
	if err := d.Set("shift-symbol-halfwidth", "all"); err != nil {
		t.Fatal(err)
	}
	for _, it := range d.Items() {
		if it.Key == "ctrl-punctuation" && (!it.Disabled || it.Effective != "on" || it.Value != "on") {
			t.Fatalf("預設開著的 Ctrl＋標點：%+v", it)
		}
	}
	if err := s.Set("shift-symbol-halfwidth", "on"); err != nil {
		t.Fatal(err)
	}
	if s.EffectiveCtrlPunctuation() || !s.EffectiveToneless() {
		t.Fatal("換回 Shift＋符號打半形後照設定檔")
	}
	for _, it := range s.Items() {
		if (it.Key == "ctrl-punctuation" || it.Key == "toneless") && (it.Disabled || it.Effective != "") {
			t.Fatalf("%s 應恢復：%+v", it.Key, it)
		}
	}
}

// 不屬於輸入習慣風格：任何內建風格都能選 all，換風格不改它；微軟參考與 Shift＋空白全形的風格照樣可選。
func TestAllSymbolsHalfwidthIsNotLockedByProfiles(t *testing.T) {
	for _, id := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
		s := freshSettings()
		if err := s.ProfileAction("select", id, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("shift-symbol-halfwidth", "all"); err != nil {
			t.Fatalf("%s 風格應可選所有符號鍵打半形：%v", id, err)
		}
		if err := s.ProfileAction("select", "0c", ""); err != nil {
			t.Fatal(err)
		}
		if v, _ := s.Value("shift-symbol-halfwidth"); v != "all" {
			t.Fatalf("%s：換風格不改標點寬度：%q", id, v)
		}
	}
}
