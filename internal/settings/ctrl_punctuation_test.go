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
		d := ctrlPunctuationDescription(goos)
		if !strings.Contains(d, "「，」") || !strings.Contains(d, "關掉") || !strings.Contains(d, "英文模式不受影響") {
			t.Fatalf("%s 的說明要寫出打什麼、可以關掉：%s", goos, d)
		}
		if goos != "darwin" && !strings.Contains(d, "VS Code") {
			t.Fatalf("%s 的說明要寫出占用哪些快速鍵：%s", goos, d)
		}
		// #231：Ctrl＋Shift＋; 9 0 打「：（）」，說明也要列出來與會占用的快速鍵。
		if !strings.Contains(d, "「：」") || !strings.Contains(d, "「（」") || !strings.Contains(d, "「）」") {
			t.Fatalf("%s 的說明要寫出冒號與括號：%s", goos, d)
		}
		if goos == "windows" && !strings.Contains(d, "Excel") || goos == "linux" && !strings.Contains(d, "Konsole") {
			t.Fatalf("%s 的說明要寫出 Ctrl＋Shift＋; 9 0 會占用的快速鍵：%s", goos, d)
		}
		// Windows 預設把 Ctrl＋Shift＋0 給輸入法直接切換熱鍵（0x104），系統先拿走、打不出「）」（.14 驗收）；只有 Windows 寫。
		const reserved = "Windows 預設把 Ctrl＋Shift＋0 留給切換輸入法，這一鍵打不出「）」"
		if strings.Contains(d, reserved) != (goos == "windows") {
			t.Fatalf("%s 的說明對 Ctrl＋Shift＋0 的系統熱鍵：%s", goos, d)
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
