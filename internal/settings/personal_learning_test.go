package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #297：「記住我選的字詞」預設開啟，舊設定檔沒有欄位時也是開；可以關、再開，讀不懂的值退回預設（開）並在存檔時保留原文。
// 鍵名與手機 App 的 personal_learning 相同。
func TestPersonalLearningDefaultsOnAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if s, err := Load(path); err != nil || !s.PersonalLearning {
		t.Fatalf("預設開啟：%+v %v", s, err)
	}
	if v, _ := Default().Value("personal-learning"); v != "on" {
		t.Fatalf("命令列預設值：%q", v)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"selection":"statistical","future":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(path); err != nil || !s.PersonalLearning || s.ReadOnly() {
		t.Fatalf("舊設定檔沒有欄位時應是開、可寫：%+v %v", s, err)
	}
	for _, value := range []string{"off", "on", "off"} {
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set("personal-learning", value); err != nil {
			t.Fatal(err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.PersonalLearning != (value == "on") || back.Selection != Statistical {
			t.Fatalf("重新讀取：%+v %v", back, err)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), `"personal_learning": `+map[string]string{"on": "true", "off": "false"}[value]) || !strings.Contains(string(b), `"future": 42`) {
			t.Fatalf("settings.json：%s", b)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"personal_learning":"no"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil || !s.PersonalLearning {
		t.Fatalf("讀不懂的值退回預設（開）：%+v %v", s, err)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"personal_learning": "no"`) {
		t.Fatalf("讀不懂的原值應保留：%s", b)
	}
	if d := Default(); d.Set("personal-learning", "maybe") == nil {
		t.Fatal("不認得的值應拒絕")
	}
	for _, it := range Default().Items() {
		if it.Key == "personal-learning" {
			if it.Value != "on" || it.Default != "on" || it.Label != "記住我選的字詞" || it.Locked || it.Disabled ||
				!strings.Contains(it.Description, "只存在這台電腦") ||
				!strings.Contains(settingsReference(t), "神經選字、依前文選字照舊") || !strings.Contains(settingsReference(t), "按「清除學習紀錄」") { // 細節在設定參考文件（#362）
				t.Fatalf("設定程式的項目：%+v", it)
			}
			return
		}
	}
	t.Fatal("設定程式看不到記住我選的字詞的開關")
}

// 個人偏好，不屬於輸入習慣風格：選用內建風格時照樣可以單獨關閉，換風格不改這個開關。
func TestPersonalLearningIsNotLockedByProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, id := range []string{"0c", "microsoft", "chewing", "mcbopomofo", "macos"} {
		s := freshSettings()
		if err := s.ProfileAction("select", id, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Set("personal-learning", "off"); err != nil {
			t.Fatalf("%s 風格應可關閉：%v", id, err)
		}
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		back, err := Load(path)
		if err != nil || back.ReadOnly() || back.PersonalLearning || back.ActiveProfile().ID != id {
			t.Fatalf("%s：重新讀取 %+v %v", id, back.ActiveProfile(), err)
		}
		if err := back.ProfileAction("select", "0c", ""); err != nil || back.PersonalLearning {
			t.Fatalf("%s：換風格不改這個開關：%v", id, err)
		}
	}
}
