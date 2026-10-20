package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #173：「Caps Lock 開著時直接打英文」預設關閉，舊設定檔沒有欄位時也是關；可以開、改回，讀不懂的值退回預設並保留原文。
// 只有 Windows、Linux 有這一項，macOS 一律打大寫英文（#84）。
func TestCapsLockEnglishDefaultsOffAndPersists(t *testing.T) {
	if !CapsLockEnglishSetting("windows") || !CapsLockEnglishSetting("linux") || CapsLockEnglishSetting("darwin") {
		t.Fatal("只有 Windows、Linux 有這一項")
	}
	if !CapsLockEnglishSetting(runtime.GOOS) {
		t.Skip("macOS 沒有這一項")
	}
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.CapsLockEnglish {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if v, _ := Default().Value("caps-lock-english"); v != "off" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"on", "off", "on"} {
		s, err := Load(path)
		if err != nil || (value == "on") == s.CapsLockEnglish {
			t.Fatalf("改之前：%+v %v", s, err)
		}
		if err := s.Set("caps-lock-english", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.CapsLockEnglish != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"caps_lock_english": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"caps_lock_english":"yes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || s.CapsLockEnglish {
		t.Fatalf("讀不懂的值退回預設：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"caps_lock_english": "yes"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("caps-lock-english", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	items := Default().Items()
	for i, it := range items {
		if it.Key == "caps-lock-english" {
			if it.Value != "off" || it.Default != "off" || !strings.Contains(it.Description, "大寫") || items[i-1].Key != "ctrl-punctuation" {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到 Caps Lock 的開關")
}
