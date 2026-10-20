package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/filelock"
)

// #169：引擎從選單切換「輸出簡體字」時照 0c-update set 的寫法寫設定檔：在同一把寫入鎖內讀改寫，保留不認得的欄位，
// 值沒變不重寫；設定檔損毀或來自較新版本時不覆寫。
func TestChangeWritesLikeUpdaterSet(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	path := Path(user)
	// 沒有設定檔、值跟預設一樣：不建立檔案。
	if changed, err := Change(user, [2]string{"output-simplified", "off"}); err != nil || changed {
		t.Fatalf("%v %v", changed, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("值沒變不該建立設定檔")
	}
	os.WriteFile(path, []byte(`{"selection":"statistical","future_key":[1,2],"output_simplified":false}`), 0o600)
	if changed, err := Change(user, [2]string{"output-simplified", "on"}); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	var got map[string]any
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &got) != nil || got["output_simplified"] != true || got["selection"] != "statistical" || got["future_key"] == nil {
		t.Fatalf("寫回的設定：%s", b)
	}
	before, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)
	if changed, err := Change(user, [2]string{"output-simplified", "on"}); err != nil || changed {
		t.Fatalf("值沒變：%v %v", changed, err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("值沒變不重寫")
	}
	// 損毀：不覆寫，留給設定程式修復。
	os.WriteFile(path, []byte(`{壞`), 0o600)
	if _, err := Change(user, [2]string{"output-simplified", "off"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("損毀：%v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{壞` {
		t.Fatal("損毀的檔案被改了")
	}
	// 較新版本寫的檔：唯讀。
	os.WriteFile(path, []byte(`{"schema":99,"output_simplified":true}`), 0o600)
	if _, err := Change(user, [2]string{"output-simplified", "off"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("較新版本：%v", err)
	}
	// 不認得的鍵：整筆不寫。
	os.WriteFile(path, []byte(`{}`), 0o600)
	if _, err := Change(user, [2]string{"no-such-key", "on"}); err == nil {
		t.Fatal("不認得的鍵要回錯誤")
	}
}

// 0c-update set 正握著寫入鎖時等它寫完，再以它寫好的檔案為準。
func TestChangeWaitsForTheWriteLock(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	unlock, err := filelock.Acquire(dir, WriteLock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Change(user, [2]string{"output-simplified", "on"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(Path(user), []byte(`{"punctuation":true}`), 0o600) // 持有鎖的一方寫好的檔
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s, err := Load(Path(user))
	if err != nil || !s.OutputSimplified || !s.Punctuation {
		t.Fatalf("%+v %v", s, err)
	}
}
