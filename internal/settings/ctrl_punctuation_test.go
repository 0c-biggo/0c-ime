package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #168：「Ctrl＋標點打全形」預設開啟，舊設定檔沒有欄位時也是開；可以關掉、改回，
// 讀不懂的值退回預設（開）並在存檔時保留原文。
func TestCtrlPunctuationDefaultsOnAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || !s.CtrlPunctuation {
		t.Fatalf("預設開啟：%+v %v", s, err)
	}
	if v, _ := Default().Value("ctrl-punctuation"); v != "on" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || !s.CtrlPunctuation {
		t.Fatalf("舊設定檔沒有欄位時應是開：%+v %v", s, err)
	}
	for _, value := range []string{"off", "on", "off"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("ctrl-punctuation", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.CtrlPunctuation != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"ctrl_punctuation": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"ctrl_punctuation":"no"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || !s.CtrlPunctuation {
		t.Fatalf("讀不懂的值退回預設：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"ctrl_punctuation": "no"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	d := Default()
	if err := d.Set("ctrl-punctuation", "maybe"); err == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		// #362：說明只有一句：舉兩個鍵、會占用程式的快速鍵（Mac 寫 ⌃、不再交給 App）。
		d := ctrlPunctuationDescription(goos)
		key := map[bool]string{true: "⌃, 打「，」", false: "Ctrl＋, 打「，」"}[goos == "darwin"]
		if !strings.Contains(d, key) || !strings.Contains(d, "「。」") || !strings.Contains(d, "快速鍵") && !strings.Contains(d, "不再交給 App") {
			t.Fatalf("%s 的說明要寫出打什麼、會占用快速鍵：%s", goos, d)
		}
	}
	// 完整的按鍵表、占用哪些快速鍵、可以關掉、各平台的例外在設定參考文件（#362）。
	ref := settingsReference(t)
	for _, want := range []string{
		"「，」", "英文模式不受影響", "可以關掉", "VS Code", "Excel", "Konsole",
		"| Ctrl＋Shift＋`;` | ： |", "| Ctrl＋Shift＋`9` | （ |", "| Ctrl＋Shift＋`0` | ） |", // #231
		// Windows 預設把 Ctrl＋Shift＋0 給輸入法直接切換熱鍵（0x104），系統先拿走、打不出「）」（.14 驗收）。
		"Windows 預設把 Ctrl＋Shift＋0 留給切換輸入法，這一鍵打不出「）」",
		"Fcitx5 的剪貼簿開著時，Ctrl＋; 照舊叫出剪貼簿",
	} {
		if !strings.Contains(ref, want) {
			t.Fatalf("設定參考文件的 Ctrl＋標點少了：%s", want)
		}
	}
	for _, it := range Default().Items() {
		if it.Key == "ctrl-punctuation" {
			if it.Value != "on" || it.Default != "on" || it.Label != "Ctrl＋標點打全形" {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到 Ctrl＋標點的開關")
}
