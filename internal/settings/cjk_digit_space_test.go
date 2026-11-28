package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #365：「自動加空格」三選一（cjk-latin-space：off／on／all）。all 存成 cjk_latin_space＋cjk_digit_space 兩個 true：
// 舊版只認得前者，讀到最接近的「中英文之間」；on、off 的意思與寫出的 cjk_latin_space 跟 #273 相同。
func TestCJKSpaceChoicePersistsAndStaysCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.CJKDigitSpace || s.CJKLatinSpace {
		t.Fatalf("預設關閉：%+v %v", s, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":2,"selection":"statistical","cjk_latin_space":true,"future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if v, _ := s.Value("cjk-latin-space"); err != nil || v != "on" || s.CJKDigitSpace {
		t.Fatalf("舊設定檔的 true 是中英文之間：%q %v", v, err)
	}
	for _, value := range []string{"all", "on", "off", "all"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("cjk-latin-space", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if v, _ := back.Value("cjk-latin-space"); err != nil || v != value || back.Selection != Statistical || back != s ||
			back.CJKLatinSpace != (value != "off") || back.CJKDigitSpace != (value == "all") {
			t.Fatalf("%s 重新讀取：%q %+v %v", value, v, back, err)
		}
		b, _ := os.ReadFile(path)
		want := map[string][2]string{"off": {"false", "false"}, "on": {"true", "false"}, "all": {"true", "true"}}[value]
		if !strings.Contains(string(b), `"cjk_latin_space": `+want[0]) || !strings.Contains(string(b), `"cjk_digit_space": `+want[1]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("%s 的 settings.json：%s", value, b)
		}
	}
	if d := Default(); d.Set("cjk-latin-space", "digits") == nil || d.Set("cjk-latin-space", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
}

// 「只補數字」不提供。cjk_digit_space 只在 cjk_latin_space 為 true 時成立：舊版把中英文關掉後留下的 false＋true
// 讀成 off，不會因為殘值變成 all；之後選 on 就是 on。
func TestCJKDigitSpaceNeedsLatinSpace(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, tc := range []struct{ file, want string }{
		{`{"schema":2,"cjk_latin_space":false,"cjk_digit_space":true}`, "off"},
		{`{"schema":2,"cjk_digit_space":true}`, "off"},
		{`{"schema":2,"cjk_latin_space":"yes","cjk_digit_space":true}`, "off"}, // 讀不懂的值退回預設（關）
		{`{"schema":2,"cjk_latin_space":true,"cjk_digit_space":"yes"}`, "on"},
		{`{"schema":2,"cjk_latin_space":true,"cjk_digit_space":false}`, "on"},
		{`{"schema":2,"cjk_latin_space":true,"cjk_digit_space":true}`, "all"},
	} {
		if err := os.WriteFile(path, []byte(tc.file), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(path)
		if v, _ := s.Value("cjk-latin-space"); err != nil || v != tc.want || s.CJKDigitSpace != (tc.want == "all") || s.CJKLatinSpace != (tc.want != "off") {
			t.Fatalf("%s：%q %+v %v", tc.file, v, s, err)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":2,"cjk_latin_space":false,"cjk_digit_space":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(path)
	if err := s.Set("cjk-latin-space", "on"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if back, err := Load(path); err != nil || back.cjkSpace() != "on" {
		t.Fatalf("殘值之後選 on 就是 on，不是 all：%+v %v", back, err)
	}
}

// 舊版（0.2.15 以前）讀新檔：它只有 cjk_latin_space 一個欄位，不認得的欄位原文留著、存檔時寫回。選 all 時舊版照 on
// 打字，新欄位不會掉；舊版把中英文關掉再存檔，新版讀到的是 off。這裡用只改舊欄位的寫法模擬；真的舊版 0c-update
// 在 scripts/test_old_updaters.py 跑。
func TestOlderVersionsReadAllAsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s := Default()
	if err := s.Set("cjk-latin-space", "all"); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var file map[string]json.RawMessage
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	if string(file["cjk_latin_space"]) != "true" || string(file["cjk_digit_space"]) != "true" {
		t.Fatalf("all 要寫成兩個 true，舊版才讀得到 on：%s", b)
	}
	// 舊版關掉中英文：只改它認得的欄位，其餘照原文寫回。
	file["cjk_latin_space"] = json.RawMessage("false")
	old, _ := json.Marshal(file)
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	if back, err := Load(path); err != nil || back.cjkSpace() != "off" {
		t.Fatalf("舊版關掉後新版讀到 off：%+v %v", back, err)
	}
}

// 設定程式的項目：一項三個選項，名稱「自動加空格」；cjk-digit-space 這個鍵沒有發布過，不存在。
func TestCJKSpaceItem(t *testing.T) {
	if _, ok := Default().Value("cjk-digit-space"); ok {
		t.Fatal("cjk-digit-space 不是設定的鍵")
	}
	if d := Default(); d.Set("cjk-digit-space", "on") == nil {
		t.Fatal("cjk-digit-space 應回報未知的設定")
	}
	for _, it := range Default().Items() {
		if it.Key != "cjk-latin-space" {
			continue
		}
		if it.Label != "自動加空格" || it.Value != "off" || it.Default != "off" || it.Locked || strings.Join(it.Values, ",") != "off,on,all" ||
			it.ValueLabels["off"] != "關閉" || it.ValueLabels["on"] != "中英文之間" || it.ValueLabels["all"] != "中英文與數字之間" {
			t.Fatalf("設定程式的項目：%+v", it)
		}
		if it.Description != "送出時在中文與英文、數字之間補半形空格，只看同一次送出的字。" {
			t.Fatalf("說明：%q", it.Description)
		}
		if _, ok := it.ValueHints["off"]; ok || !strings.Contains(it.ValueHints["on"], "「用 Python 寫」") || !strings.Contains(it.ValueHints["on"], "數字旁不補") ||
			!strings.Contains(it.ValueHints["all"], "「第 2 版」") || !strings.Contains(it.ValueHints["all"], "沒在組字時打的數字不補") {
			t.Fatalf("選項的提示：%+v", it.ValueHints)
		}
		for value, hint := range it.ValueHints {
			if w := textWidth(hint); w > 35*2 {
				t.Errorf("%s 的提示超過 35 個全形字（%d.%d）：%q", value, w/2, w%2*5, hint)
			}
		}
		return
	}
	t.Fatal("設定程式看不到自動加空格")
}

// 設定參考文件寫明限制：數字要在組字裡打才補得到、百分號算數字的一部分、片語照原樣、只看同一次送出。
func TestCJKSpaceReferenceStatesTheLimits(t *testing.T) {
	ref := settingsReference(t)
	at := strings.Index(ref, "\n### 自動加空格\n")
	if at < 0 {
		t.Fatal("設定參考文件沒有「自動加空格」一節")
	}
	body := ref[at:]
	body = body[:strings.Index(body[1:], "\n### ")+1]
	for _, want := range []string{"錨點：`cjk-latin-space`", "中英文之間", "中英文與數字之間", "「用 Python 寫」", "第 2 版」", "「有 50% 的人」",
		"沒在組字時打的數字", "「2026年 11 月 25 日」", "我的片語", "照原樣送出", "只看這一次送出的字", "`off`、`on`、`all`"} {
		if !strings.Contains(body, want) {
			t.Errorf("設定參考文件「自動加空格」一節要寫到 %q", want)
		}
	}
}
