package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.2.0 正式開放（#128）：舊檔裡的 preview_0_2 不論 true、false 或讀不懂的值都忽略，存檔時移除；
// 其他欄位照舊讀寫，也不因此出現「本版不認得」的提示。
func TestPreview02RetiredKeyDroppedOnSave(t *testing.T) {
	for _, raw := range []string{`false`, `true`, `"beta"`} {
		path := filepath.Join(t.TempDir(), FileName)
		old := `{"schema":1,"selection":"statistical","preview_0_2":` + raw + `,"later_field":7}`
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Read(path)
		if err != nil {
			t.Fatal(err)
		}
		if r.Settings.Selection != Statistical {
			t.Fatalf("%s：其他欄位照舊讀取：%+v", raw, r.Settings)
		}
		for _, note := range r.Notes {
			if strings.Contains(note, "preview") {
				t.Fatalf("%s：已拿掉的鍵不當成本版不認得的欄位：%v", raw, r.Notes)
			}
		}
		if v, ok := r.Settings.Value(PreviewRetiredKey); !ok || v != "on" {
			t.Fatalf("%s：舊檔寫 %s 仍為開放：%q %v", raw, raw, v, ok)
		}
		if err := Save(path, r.Settings); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "preview") {
			t.Fatalf("%s：寫回不含 preview_0_2：%s", raw, b)
		}
		if !strings.Contains(string(b), `"later_field": 7`) || !strings.Contains(string(b), `"selection": "statistical"`) {
			t.Fatalf("%s：其他欄位照舊保留：%s", raw, b)
		}
	}
}

// set preview-0-2 on 仍成功但不改任何東西（舊腳本照常能跑），off 回清楚的錯誤；設定程式與命令列說明都不列這一項。
func TestPreview02SetIsRetired(t *testing.T) {
	for _, it := range Default().Items() {
		if it.Key == PreviewRetiredKey {
			t.Fatal("設定程式不顯示已拿掉的開關")
		}
	}
	if _, ok := Keys()[PreviewRetiredKey]; ok {
		t.Fatal("命令列說明不列已拿掉的開關")
	}
	var unknown *SetError
	if err := (&Settings{}).Set("nope", "on"); !errors.As(err, &unknown) || unknown.Code != CodeUnknownKey || strings.Contains(err.Error(), "preview") {
		t.Fatalf("未知鍵的提示不列已拿掉的開關：%v", err)
	}

	s := Default()
	before := s
	if err := s.Set(PreviewRetiredKey, "on"); err != nil {
		t.Fatalf("on 照舊成功：%v", err)
	}
	if s != before {
		t.Fatalf("on 不改任何設定：%+v", s)
	}
	var invalid *SetError
	err := s.Set(PreviewRetiredKey, "off")
	if !errors.As(err, &invalid) || invalid.Code != CodeInvalidValue || !strings.Contains(err.Error(), "正式開放") {
		t.Fatalf("off 回清楚的錯誤：%v", err)
	}
	if s != before {
		t.Fatalf("off 失敗時不改設定：%+v", s)
	}
}
