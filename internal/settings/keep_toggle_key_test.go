package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #167：升級的人保留右 Shift。只補 toggle_key 一個欄位，已記下的選擇、舊的 left_shift 與無法處理的檔案都不動。
func TestKeepToggleKey(t *testing.T) {
	cases := []struct {
		name   string
		body   *string // nil：沒有設定檔
		wrote  bool
		want   string // 寫完後讀到的切換鍵；空字串表示不檢查
		member bool   // 寫完後檔案裡有 toggle_key
	}{
		{name: "沒有設定檔", body: nil, wrote: true, want: ToggleRight, member: true},
		{name: "空物件", body: ptr(`{}`), wrote: true, want: ToggleRight, member: true},
		{name: "其他欄位都在但沒有切換鍵", body: ptr("{\n  \"schema\": 1,\n  \"punctuation\": true,\n  \"future_key\": [1, 2]\n}\n"), wrote: true, want: ToggleRight, member: true},
		{name: "舊檔 left_shift=false", body: ptr(`{"left_shift":false}`), wrote: true, want: ToggleRight, member: true},
		{name: "舊檔 left_shift=true 已推出 both", body: ptr(`{"left_shift":true}`), wrote: false, want: ToggleBoth},
		{name: "使用者選了不用 Shift", body: ptr(`{"toggle_key":"none"}`), wrote: false, want: ToggleNone, member: true},
		{name: "使用者選了左 Shift", body: ptr(`{"toggle_key":"left","left_shift":true}`), wrote: false, want: ToggleLeft, member: true},
		{name: "本版讀不懂的切換鍵值", body: ptr(`{"toggle_key":"ctrl"}`), wrote: false, member: true},
		{name: "較新版本的設定檔", body: ptr(`{"schema":99}`), wrote: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if c.body != nil {
				if err := os.WriteFile(path, []byte(*c.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wrote, err := KeepToggleKey(path, ToggleRight)
			if err != nil || wrote != c.wrote {
				t.Fatalf("KeepToggleKey = %v, %v；預期 %v", wrote, err, c.wrote)
			}
			b, err := os.ReadFile(path)
			if c.body == nil && !c.wrote {
				if !os.IsNotExist(err) {
					t.Fatal("不該建立設定檔")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !c.wrote && string(b) != *c.body {
				t.Fatalf("沒有要改的檔案被動過：%s", b)
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal(b, &members); err != nil {
				t.Fatalf("寫出的不是 JSON：%v\n%s", err, b)
			}
			if _, ok := members["toggle_key"]; ok != c.member {
				t.Fatalf("toggle_key 在檔案裡＝%v：%s", ok, b)
			}
			if c.body != nil && c.wrote {
				// 原本的內容逐字保留，只在最後多一個成員；未知欄位與 schema 不變。
				if !strings.HasPrefix(string(b), strings.TrimRight(strings.TrimSpace(*c.body), "}")) {
					t.Fatalf("原內容沒有逐字保留：%s", b)
				}
				if len(members) != countMembers(t, *c.body)+1 {
					t.Fatalf("只該多 toggle_key：%s", b)
				}
			}
			if c.body == nil && c.wrote && (len(members) != 2 || string(members["schema"]) != "2") {
				t.Fatalf("缺檔時只寫 schema 與 toggle_key，其他維持預設：%s", b)
			}
			if c.want != "" {
				s, err := Load(path)
				if err != nil || s.ToggleKey != c.want {
					t.Fatalf("讀回 %q（%v），預期 %q", s.ToggleKey, err, c.want)
				}
			}
			// 跑第二次不再寫：寫過之後檔案已有 toggle_key。
			again, err := KeepToggleKey(path, ToggleRight)
			if err != nil || again {
				t.Fatalf("第二次 = %v, %v", again, err)
			}
		})
	}
}

// 寫過之後使用者自己改成別的值，之後的更新不會蓋回去。
func TestKeepToggleKeyOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if wrote, err := KeepToggleKey(path, ToggleRight); err != nil || !wrote {
		t.Fatal(wrote, err)
	}
	s, _ := Load(path)
	if err := s.Set("toggle-key", ToggleNone); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if wrote, err := KeepToggleKey(path, ToggleRight); err != nil || wrote {
		t.Fatal(wrote, err)
	}
	if s, _ := Load(path); s.ToggleKey != ToggleNone {
		t.Fatalf("使用者的選擇被蓋掉：%s", s.ToggleKey)
	}
}

func TestKeepToggleKeyLeavesCorruptFile(t *testing.T) {
	for _, body := range []string{`not json`, `[1]`, `null`} {
		path := filepath.Join(t.TempDir(), FileName)
		os.WriteFile(path, []byte(body), 0o600)
		wrote, err := KeepToggleKey(path, ToggleRight)
		if wrote || !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%s：%v %v", body, wrote, err)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Fatalf("壞檔被改：%s", b)
		}
	}
	if _, err := KeepToggleKey(filepath.Join(t.TempDir(), FileName), "ctrl"); err == nil {
		t.Fatal("不認得的值")
	}
}

func ptr(s string) *string { return &s }

func countMembers(t *testing.T, body string) int {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	return len(m)
}
