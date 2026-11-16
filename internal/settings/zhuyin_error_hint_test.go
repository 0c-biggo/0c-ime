package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #300：「打錯注音時提示」預設開啟，舊設定檔沒有欄位時也是開；可以關、再開，讀不懂的值退回預設（開）並在存檔時保留原文。
func TestZhuyinErrorHintDefaultsOnAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || !s.ZhuyinErrorHint {
		t.Fatalf("預設開啟：%+v %v", s, err)
	}
	if v, _ := Default().Value("zhuyin-error-hint"); v != "on" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || !s.ZhuyinErrorHint || s.ReadOnly() {
		t.Fatalf("舊設定檔沒有欄位時應是開、可寫：%+v %v", s, err)
	}
	for _, value := range []string{"off", "on", "off"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("zhuyin-error-hint", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.ZhuyinErrorHint != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"zhuyin_error_hint": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"zhuyin_error_hint":"no"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || !s.ZhuyinErrorHint {
		t.Fatalf("讀不懂的值退回預設（開）：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"zhuyin_error_hint": "no"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("zhuyin-error-hint", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, it := range Default().Items() {
		if it.Key == "zhuyin-error-hint" {
			if it.Value != "on" || it.Default != "on" || it.Label != "打錯注音時提示" || it.Locked || it.Disabled ||
				!strings.Contains(it.Description, "ㄅㄩˊ") || !strings.Contains(it.Description, "再按一次同一個聲調") {
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到打錯注音時提示的開關")
}

// 說明依平台：Windows、macOS 發出提示音，Linux（Fcitx5、IBus 沒有統一的提示音）標示注音。
func TestZhuyinErrorHintDescriptionPerPlatform(t *testing.T) {
	for goos, want := range map[string]string{"windows": "提示音", "darwin": "提示音", "linux": "標示出來"} {
		if d := zhuyinErrorHintDescription(goos); !strings.Contains(d, want) {
			t.Errorf("%s：%q 應寫 %q", goos, d, want)
		}
	}
}

// 中英混打實際開著、或不打聲調實際開著時不作用（停用並說明原因、指向造成的那一項）；兩者本身停用時照常。
func TestZhuyinErrorHintDisabledByMixedInputAndToneless(t *testing.T) {
	item := func(s Settings) Item {
		for _, it := range s.Items() {
			if it.Key == "zhuyin-error-hint" {
				return it
			}
		}
		t.Fatal("沒有 zhuyin-error-hint")
		return Item{}
	}
	s := freshSettings()
	if it := item(s); it.Disabled {
		t.Fatalf("預設可用：%+v", it)
	}
	for _, c := range []struct {
		name    string
		set     [][2]string
		reason  string
		causeBy string
	}{
		{"中英混打", [][2]string{{"mixed-input", "on"}}, zhuyinHintMixedReason, "mixed-input"},
		{"不打聲調", [][2]string{{"toneless", "on"}}, "不打聲調時關閉", "toneless"},
		{"兩者都開時寫不打聲調（混打已被它停用）", [][2]string{{"mixed-input", "on"}, {"toneless", "on"}}, "不打聲調時關閉", "toneless"},
		{"許氏時混打停用：照常", [][2]string{{"mixed-input", "on"}, {"keyboard-layout", LayoutHsu}}, "", ""},
		{"倚天時不打聲調停用：照常", [][2]string{{"toneless", "on"}, {"keyboard-layout", LayoutETen}}, "", ""},
		{"所有符號鍵打半形時不打聲調暫停：照常", [][2]string{{"toneless", "on"}, {"shift-symbol-halfwidth", "all"}}, "", ""},
	} {
		s := Default() // 我的原有設定：混打、不打聲調都能改（內建風格把它們固定住）
		for _, kv := range c.set {
			if err := s.Set(kv[0], kv[1]); err != nil {
				t.Fatalf("%s：%v", c.name, err)
			}
		}
		it := item(s)
		if it.Disabled != (c.reason != "") || it.DisabledReason != c.reason || it.DisabledBy != c.causeBy || it.Value != "on" {
			t.Errorf("%s：%+v", c.name, it)
		}
	}
}

// 不屬於輸入習慣風格：選用內建風格時照樣可以單獨開關，換風格不改這個開關。
func TestZhuyinErrorHintIsNotLockedByProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, id := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
		s := freshSettings()
		if err := s.ProfileAction("select", id, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("zhuyin-error-hint", "off"); err != nil {
			t.Fatalf("%s 風格應可關閉：%v", id, err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.ReadOnly() || back.ZhuyinErrorHint || back.ActiveProfile().ID != id {
			t.Fatalf("%s：重新讀取 %+v %v", id, back.ActiveProfile(), err)
		}
		if err := back.ProfileAction("select", "0c", ""); err != nil || back.ZhuyinErrorHint {
			t.Fatalf("%s：換風格不改這個開關：%v", id, err)
		}
	}
}
