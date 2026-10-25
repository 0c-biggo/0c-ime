package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/filelock"
)

// BothShiftMarker 是 Windows 已經把舊的右 Shift 換成左右 Shift 的紀錄（#206），放在設定檔旁的 update 目錄
// （%LOCALAPPDATA%\Zuin\update，與更新器的狀態同一處）。
func BothShiftMarker(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "update", "toggle-key-both")
}

// AdoptBothShift（#206）：Windows 的中英切換鍵預設從右 Shift 改成左右 Shift，沒有自己選過的舊用戶也跟著改。
// 0.2.4 以前每次存檔都把當時的預設 right 寫進 toggle_key（改其他設定時也是），檔案分不出那是使用者選的還是順手寫進去的，
// 所以第一次以新版啟動時把 toggle_key=right 改成 both 一次，並記下 marker；之後使用者自己改回右 Shift 不會再被改掉。
// 沒有 toggle_key 的舊檔（或只有 left_shift=false）讀起來本來就是新預設，不必改寫；left／none／both 與讀不懂的值都保留。
// 只改 toggle_key 與舊欄位 left_shift 的值，其他欄位（含本版不認得的）照原樣、照原順序寫回。
// 較新版本的檔案不動；讀不到或無法解析時回傳錯誤、不記 marker，下次再試。
// userStore 是個人詞庫 user.json；在設定檔的寫入鎖內讀改寫（同 Change）。
func AdoptBothShift(userStore string) (bool, error) {
	path := Path(userStore)
	if _, err := os.Stat(BothShiftMarker(path)); err == nil {
		return false, nil
	}
	wrote := false
	err := filelock.With(filepath.Dir(userStore), WriteLock, func() error {
		b, missing, err := readFile(path)
		if err != nil {
			return err
		}
		if !missing {
			if wrote, err = adoptBothShift(path, b); err != nil {
				return err
			}
		}
		return MarkBothShift(path)
	})
	return wrote, err
}

// MarkBothShift 記下這個設定檔已經不必再換成左右 Shift：Windows 的使用者自己設定中英切換鍵時呼叫（#206），
// 新版第一次啟動前就選了右 Shift 的人不會被改掉。
func MarkBothShift(path string) error {
	marker := BothShiftMarker(path)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("206\n"), 0o600)
}

func adoptBothShift(path string, b []byte) (bool, error) {
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return false, fmt.Errorf("%s：%w：%v", path, ErrCorrupt, err)
	}
	if s.readOnly || s.profiles != "" {
		return false, nil
	}
	members, err := orderedMembers(b)
	if err != nil {
		return false, fmt.Errorf("%s：%w：%v", path, ErrCorrupt, err)
	}
	found := false
	for _, m := range members {
		if m.key == "toggle_key" {
			var v string
			if json.Unmarshal(m.raw, &v) != nil || v != ToggleRight {
				return false, nil
			}
			found = true
		}
	}
	if !found {
		return false, nil
	}
	both, _ := json.Marshal(ToggleBoth)
	var out bytes.Buffer
	out.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			out.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		out.Write(k)
		out.WriteByte(':')
		switch {
		case m.key == "toggle_key":
			out.Write(both)
		case m.key == "left_shift" && bytes.Equal(bytes.TrimSpace(m.raw), []byte("false")):
			out.WriteString("true") // 給讀 left_shift 的舊版：左 Shift 也切換
		default:
			if err := json.Compact(&out, m.raw); err != nil {
				return false, fmt.Errorf("%s：%w：%v", path, ErrCorrupt, err)
			}
		}
	}
	out.WriteByte('}')
	var indented bytes.Buffer
	if err := json.Indent(&indented, out.Bytes(), "", "  "); err != nil {
		return false, fmt.Errorf("%s：%w：%v", path, ErrCorrupt, err)
	}
	indented.WriteByte('\n')
	// 寫入前再核對一次：只有切換鍵變了。
	var check Settings
	if json.Unmarshal(indented.Bytes(), &check) != nil || check.ToggleKey != ToggleBoth || check.readOnly {
		return false, fmt.Errorf("%s：%w", path, ErrCorrupt)
	}
	return true, writeAtomic(path, indented.Bytes())
}

type member struct {
	key string
	raw json.RawMessage
}

// orderedMembers 依檔案中的順序取出最上層的成員。
func orderedMembers(b []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("不是 JSON 物件")
	}
	var out []member
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := t.(string)
		if !ok {
			return nil, errors.New("不是 JSON 物件")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, member{key, raw})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, errors.New("不是 JSON 物件")
	}
	return out, nil
}
