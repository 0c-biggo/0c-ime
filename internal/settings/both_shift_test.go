package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.2.4 在 Windows 存檔時寫出的設定檔：改過選字鍵，toggle_key 是當時的預設 right（#206）。
const savedBy024 = `{
  "schema": 1,
  "selection": "neural",
  "punctuation": false,
  "document_context": true,
  "mixed_input": false,
  "english_autocomplete": false,
  "left_shift": false,
  "esc_clear": true,
  "space_selects": true,
  "candidate_cursor": "after",
  "advance_after_select": false,
  "candidate_layout": "vertical",
  "emoji_shortcut": "equals",
  "emoji_equals_first": true,
  "keyboard_layout": "standard",
  "selection_keys": "asdfghjkl;",
  "toggle_key": "right",
  "mode_memory": "native",
  "start_mode": "zhuyin",
  "output_simplified": false,
  "simplified_hotkey": false,
  "ctrl_punctuation": true,
  "caps_lock_english": false,
  "fuzzy": {"zh-z": "on", "future": 3},
  "future_key": [1, 2]
}
`

// #206：Windows 的舊用戶第一次以新版啟動時，toggle_key=right 換成 both 一次；自己選的 left／none／both 與讀不懂的值不動。
func TestAdoptBothShift(t *testing.T) {
	cases := []struct {
		name  string
		body  *string // nil：沒有設定檔
		wrote bool
		want  string // 寫完後檔案裡的 toggle_key；空字串表示沒有這個欄位
	}{
		{name: "沒有設定檔", body: nil},
		{name: "0.2.4 存過檔（right 是當時的預設）", body: ptr(savedBy024), wrote: true, want: ToggleBoth},
		{name: "只有 toggle_key=right", body: ptr(`{"toggle_key":"right"}`), wrote: true, want: ToggleBoth},
		{name: "dev.63 以前的檔案 left_shift=false", body: ptr(`{"schema":1,"left_shift":false}`)},
		{name: "dev.63 以前的檔案 left_shift=true", body: ptr(`{"left_shift":true}`)},
		{name: "選了左右 Shift", body: ptr(`{"toggle_key":"both","left_shift":true}`), want: ToggleBoth},
		{name: "選了左 Shift", body: ptr(`{"toggle_key":"left","left_shift":true}`), want: ToggleLeft},
		{name: "選了不用 Shift", body: ptr(`{"toggle_key":"none","left_shift":false}`), want: ToggleNone},
		{name: "本版讀不懂的切換鍵值", body: ptr(`{"toggle_key":"ctrl"}`), want: "ctrl"},
		{name: "較新版本的設定檔", body: ptr(`{"schema":99,"toggle_key":"right"}`), want: ToggleRight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			user := filepath.Join(dir, "user.json")
			path := Path(user)
			if c.body != nil {
				if err := os.WriteFile(path, []byte(*c.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wrote, err := AdoptBothShift(user)
			if err != nil || wrote != c.wrote {
				t.Fatalf("AdoptBothShift = %v, %v；預期 %v", wrote, err, c.wrote)
			}
			if _, err := os.Stat(BothShiftMarker(path)); err != nil {
				t.Fatalf("判斷過要記下來，之後不再改：%v", err)
			}
			b, err := os.ReadFile(path)
			if c.body == nil {
				if !os.IsNotExist(err) {
					t.Fatal("沒有設定檔時不該建立（新預設就是左右 Shift）")
				}
				return
			}
			if !c.wrote && string(b) != *c.body {
				t.Fatalf("沒有要改的檔案被動過：%s", b)
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal(b, &members); err != nil {
				t.Fatalf("寫出的不是 JSON：%v\n%s", err, b)
			}
			var got string
			if raw, ok := members["toggle_key"]; ok {
				json.Unmarshal(raw, &got)
			}
			if got != c.want {
				t.Fatalf("toggle_key = %q，預期 %q：%s", got, c.want, b)
			}
			if c.wrote {
				var before map[string]json.RawMessage
				json.Unmarshal([]byte(*c.body), &before)
				if len(members) != len(before) {
					t.Fatalf("欄位數變了：%s", b)
				}
				for key, raw := range before {
					if key == "toggle_key" || key == "left_shift" {
						continue
					}
					if compact(t, raw) != compact(t, members[key]) {
						t.Fatalf("%s 被改了：%s → %s", key, raw, members[key])
					}
				}
				if raw, ok := members["left_shift"]; ok && string(raw) != "true" {
					t.Fatalf("給舊版讀的 left_shift 也要跟著：%s", b)
				}
				s, err := Load(path)
				if err != nil || s.ToggleKey != ToggleBoth || !s.TogglesLeft() || !s.TogglesRight() {
					t.Fatalf("讀回 %+v（%v）", s, err)
				}
			}
		})
	}
}

// 0.2.4 寫出的檔案，其他欄位與順序照原樣保留（格式同 Save），只換切換鍵。
func TestAdoptBothShiftKeepsLayout(t *testing.T) {
	user := filepath.Join(t.TempDir(), "user.json")
	path := Path(user)
	os.WriteFile(path, []byte(savedBy024), 0o600)
	if wrote, err := AdoptBothShift(user); err != nil || !wrote {
		t.Fatal(wrote, err)
	}
	b, _ := os.ReadFile(path)
	want := savedBy024
	for _, r := range [][2]string{{`"left_shift": false`, `"left_shift": true`}, {`"toggle_key": "right"`, `"toggle_key": "both"`},
		{`"fuzzy": {"zh-z": "on", "future": 3}`, "\"fuzzy\": {\n    \"zh-z\": \"on\",\n    \"future\": 3\n  }"}, {`"future_key": [1, 2]`, "\"future_key\": [\n    1,\n    2\n  ]"}} {
		want = replaceOnce(t, want, r[0], r[1])
	}
	if string(b) != want {
		t.Fatalf("寫出：\n%s\n預期：\n%s", b, want)
	}
	s, _ := Load(path)
	if s.SelectionKeys != SelectHome || !s.Fuzzy.hasUnknown() {
		t.Fatalf("其他設定要保留：%+v", s)
	}
}

// 換過一次之後，使用者自己改回右 Shift，下次啟動不再改；新版之前就自己選了右 Shift（MarkBothShift）也不改。
func TestAdoptBothShiftOnlyOnce(t *testing.T) {
	user := filepath.Join(t.TempDir(), "user.json")
	path := Path(user)
	os.WriteFile(path, []byte(`{"toggle_key":"right"}`), 0o600)
	if wrote, err := AdoptBothShift(user); err != nil || !wrote {
		t.Fatal(wrote, err)
	}
	s, _ := Load(path)
	if err := s.Set("toggle-key", ToggleRight); err != nil {
		t.Fatal(err)
	}
	Save(path, s)
	if wrote, err := AdoptBothShift(user); err != nil || wrote {
		t.Fatal(wrote, err)
	}
	if s, _ := Load(path); s.ToggleKey != ToggleRight {
		t.Fatalf("使用者的選擇被蓋掉：%s", s.ToggleKey)
	}

	user = filepath.Join(t.TempDir(), "user.json")
	path = Path(user)
	os.WriteFile(path, []byte(`{"toggle_key":"right"}`), 0o600)
	if err := MarkBothShift(path); err != nil {
		t.Fatal(err)
	}
	if wrote, err := AdoptBothShift(user); err != nil || wrote {
		t.Fatal(wrote, err)
	}
}

// 壞檔不動也不記下，修好之後下次啟動再判斷。
func TestAdoptBothShiftLeavesCorruptFile(t *testing.T) {
	for _, body := range []string{`not json`, `[1]`, `null`, `{"toggle_key":"right"`} {
		user := filepath.Join(t.TempDir(), "user.json")
		path := Path(user)
		os.WriteFile(path, []byte(body), 0o600)
		wrote, err := AdoptBothShift(user)
		if wrote || !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%s：%v %v", body, wrote, err)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Fatalf("壞檔被改：%s", b)
		}
		if _, err := os.Stat(BothShiftMarker(path)); err == nil {
			t.Fatalf("%s：壞檔不該記下已判斷", body)
		}
	}
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	i := strings.Index(s, old)
	if i < 0 {
		t.Fatalf("找不到 %q", old)
	}
	return s[:i] + new + s[i+len(old):]
}
