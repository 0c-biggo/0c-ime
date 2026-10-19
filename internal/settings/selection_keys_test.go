package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

// #112：選字鍵三選一。舊檔沒有這個欄位時是 1234567890（跟以前一樣）；讀不懂的值退回預設、存檔時原樣寫回。
func TestSelectionKeysDefaultAndCompat(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"selection":"neural"}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.SelectionKeys != SelectDigits || Default().SelectionKeys != SelectDigits {
		t.Fatalf("舊檔沒有欄位：%+v", s)
	}
	for _, v := range []string{SelectQwer, SelectHome} {
		json.Unmarshal([]byte(`{"selection_keys":"`+v+`"}`), &s)
		if s.SelectionKeys != v {
			t.Fatal(v)
		}
		b, _ := json.Marshal(s)
		if !strings.Contains(string(b), `"selection_keys":"`+v+`"`) {
			t.Fatalf("寫回：%s", b)
		}
	}
	json.Unmarshal([]byte(`{"selection_keys":"qwertyuiop"}`), &s)
	if s.SelectionKeys != SelectDigits {
		t.Fatal("讀不懂的值退回預設")
	}
	if b, _ := json.Marshal(s); !strings.Contains(string(b), `"selection_keys":"qwertyuiop"`) {
		t.Fatalf("讀不懂的值原樣寫回：%s", b)
	}
	if notes := strings.Join(s.notes(), "\n"); !strings.Contains(notes, "selection-keys") {
		t.Fatalf("說明退回：%s", notes)
	}
	if err := s.Set("selection-keys", SelectQwer); err != nil || s.SelectionKeys != SelectQwer {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(s); strings.Contains(string(b), "qwertyuiop") {
		t.Fatalf("明確設定後不再寫回舊值：%s", b)
	}
	if err := s.Set("selection-keys", "qwertyuiop"); err == nil {
		t.Fatal("不開放自訂")
	}
	if v, ok := s.Value("selection-keys"); !ok || v != SelectQwer {
		t.Fatal(v)
	}
}

// 設定程式：三個選項都有顯示名稱（每個鍵隔一格，看得出是哪一排）。
func TestSelectionKeysItem(t *testing.T) {
	for _, item := range Default().Items() {
		if item.Key != "selection-keys" {
			continue
		}
		if item.Label != "選字鍵" || item.Default != SelectDigits || len(item.Values) != 3 ||
			item.ValueLabels[SelectQwer] != "1 2 3 4 q w e r a s" || item.ValueLabels[SelectHome] != "a s d f g h j k l ;" {
			t.Fatalf("%+v", item)
		}
		return
	}
	t.Fatal("沒有 selection-keys")
}
