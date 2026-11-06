package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #258：「Shift＋符號打半形」預設關閉，舊設定檔沒有欄位時也是關；可以開、關，讀不懂的值退回預設（關）並在存檔時保留原文。
func TestShiftSymbolHalfwidthDefaultsOffAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.ShiftSymbolHalfwidth {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("shift-symbol-halfwidth"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.ShiftSymbolHalfwidth || s.ReadOnly() {
		t.Fatalf("舊設定檔沒有欄位時應是關、可寫：%+v %v", s, err)
	}
	for _, value := range []string{"on", "off", "on"} {
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
		if err != nil || back.ShiftSymbolHalfwidth != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"shift_symbol_halfwidth": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"shift_symbol_halfwidth":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.ShiftSymbolHalfwidth {
		t.Fatalf("讀不懂的值退回預設：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"shift_symbol_halfwidth": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("shift-symbol-halfwidth", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		d := shiftSymbolHalfwidthDescription(goos)
		ctrl := map[bool]string{true: "⌃", false: "Ctrl＋"}[goos == "darwin"]
		if !strings.Contains(d, "Shift＋, 打半形逗號") || !strings.Contains(d, "Shift＋9 打 (") || !strings.Contains(d, "< >") || !strings.Contains(d, ctrl+", 打「，」") || !strings.Contains(d, "[ ] 照舊打「」") || !strings.Contains(d, "微軟參考時，< > ? \" 照舊打按鍵上的字") {
			t.Fatalf("%s 的說明要寫出打什麼、全形怎麼打、[ ] 不變、微軟參考照風格（#272）：%s", goos, d)
		}
	}
	for _, it := range Default().Items() {
		if it.Key == "shift-symbol-halfwidth" {
			if it.Value != "off" || it.Default != "off" || it.Label != "Shift＋符號打半形" || it.Locked {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到 Shift＋符號打半形的開關")
}

// 不屬於輸入習慣風格：選用內建風格（新安裝預設的靈犀注音、微軟參考等）時照樣可以單獨開關，
// 已存的風格不必多一個值，舊版讀到也只是多一個不認得的欄位。
func TestShiftSymbolHalfwidthIsNotLockedByProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, id := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
		s := freshSettings()
		if err := s.ProfileAction("select", id, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("shift-symbol-halfwidth", "on"); err != nil {
			t.Fatalf("%s 風格應可開啟：%v", id, err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.ReadOnly() || !back.ShiftSymbolHalfwidth || back.ActiveProfile().ID != id {
			t.Fatalf("%s：重新讀取 %+v %v", id, back.ActiveProfile(), err)
		}
		if err := back.ProfileAction("select", "0c", ""); err != nil || !back.ShiftSymbolHalfwidth {
			t.Fatalf("%s：換風格不改這個開關：%v", id, err)
		}
	}
}
