package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #319：單按 Shift 切換中英的時限可選 200／300／400／500／600 毫秒，預設 300（舊設定檔沒有欄位時也是）；
// 讀不懂的值退回預設、存檔時保留原文；命令列不接受選單以外的值。
func TestShiftTapDefaultsTo300AndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || s.ShiftTapMillis() != 300 {
		t.Fatalf("預設 300：%+v %v", s, err)
	}
	if v, _ := Default().Value("shift-tap-ms"); v != "300" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || s.ShiftTapMillis() != 300 || s.ReadOnly() {
		t.Fatalf("舊設定檔沒有欄位時是 300、可寫：%+v %v", s, err)
	}
	for _, value := range []string{"200", "400", "500", "600", "300"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("shift-tap-ms", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if v, _ := back.Value("shift-tap-ms"); err != nil || v != value || back.Selection != Statistical {
			t.Fatalf("重新讀取 %s：%q %+v %v", value, v, back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"shift_tap_ms": `+value) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	for _, bad := range []string{`"400"`, `350`, `0`, `-1`, `true`} {
		if err := os.WriteFile(path, []byte(`{"schema":1,"shift_tap_ms":`+bad+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		r, err := Read(path)
		if err != nil || r.Settings.ShiftTapMillis() != 300 {
			t.Fatalf("讀不懂的 %s 退回預設：%+v %v", bad, r.Settings, err)
		}
		if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "shift-tap-ms") {
			t.Fatalf("讀不懂的 %s 要在 notes 說明：%q", bad, r.Notes)
		}
		if err := Save(path, r.Settings); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"shift_tap_ms": `+bad) {
			t.Fatalf("讀不懂的原值應保留：%s", b)
		}
	}
	for _, bad := range []string{"350", "100", "700", "0.3", "", "300ms"} {
		d := Default()
		err := d.Set("shift-tap-ms", bad)
		if se, ok := err.(*SetError); !ok || se.Code != CodeInvalidValue {
			t.Fatalf("選單以外的值 %q 應拒絕：%v", bad, err)
		}
		if d.ShiftTapMillis() != 300 {
			t.Fatalf("拒絕後不改值：%d", d.ShiftTapMillis())
		}
	}
	if (Settings{}).ShiftTapMillis() != 300 {
		t.Fatal("零值設定也回報 300，外殼不會拿到 0")
	}
}

// 設定程式的「中英切換」頁顯示這一項：選項名稱寫秒數；不用 Shift 切換時不作用（停用、指向中英切換鍵）；
// 不屬於輸入習慣風格，內建風格也不鎖。中英切換鍵的說明不再寫死 0.3 秒。
func TestShiftTapItem(t *testing.T) {
	s := Default()
	s.setToggleKey(ToggleRight)
	var tap, toggle *Item
	items := s.Items()
	for i := range items {
		switch items[i].Key {
		case "shift-tap-ms":
			tap = &items[i]
		case "toggle-key":
			toggle = &items[i]
		}
	}
	if tap == nil || toggle == nil {
		t.Fatal("沒有 shift-tap-ms 或 toggle-key")
	}
	if tap.Default != "300" || tap.Value != "300" || strings.Join(tap.Values, ",") != "200,300,400,500,600" || tap.Disabled || tap.Locked {
		t.Fatalf("%+v", *tap)
	}
	if tap.ValueLabels["200"] != "0.2 秒" || tap.ValueLabels["300"] != "0.3 秒" || tap.ValueLabels["600"] != "0.6 秒" {
		t.Fatalf("選項名稱：%v", tap.ValueLabels)
	}
	// #362：中英切換鍵的說明只有一句，仍然不寫死秒數；按住超過時限不算切換寫在設定參考文件，指向時限那一項。
	if strings.Contains(toggle.Description, "秒") {
		t.Fatalf("中英切換鍵的說明不寫死秒數：%s", toggle.Description)
	}
	if ref := settingsReference(t); !strings.Contains(ref, "按住 Shift 超過「"+tap.Label+"」") {
		t.Fatal("設定參考文件的中英切換鍵要指向時限設定")
	}
	for _, key := range profileKeys {
		if key == "shift-tap-ms" {
			t.Fatal("不屬於輸入習慣風格")
		}
	}
	none := Default()
	none.setToggleKey(ToggleNone)
	for _, it := range none.Items() {
		if it.Key == "shift-tap-ms" && (!it.Disabled || it.DisabledBy != "toggle-key" || it.Value != "300") {
			t.Fatalf("不用 Shift 時停用並指向中英切換鍵：%+v", it)
		}
	}
	if err := none.Set("shift-tap-ms", "500"); err != nil || none.ShiftTapMillis() != 500 {
		t.Fatalf("停用時照樣能改，改回用 Shift 就生效：%v %d", err, none.ShiftTapMillis())
	}
}
