package settings

import (
	"path/filepath"

	"git.biggo.com/0c/ime/internal/filelock"
)

// WriteLock 是讀改寫設定檔時持有的鎖（個人資料目錄的 update/settings-write.lock）。0c-update set 與
// 0c-server（從輸入法選單切換「輸出簡體字」，#169）都在這把鎖內讀改寫，彼此不會蓋掉對方剛寫的值。
const WriteLock = "settings-write.lock"

// Change 在寫入鎖內讀出 userStore（個人詞庫 user.json）旁的設定檔，依序套用鍵值，有改變才存檔；回傳是否寫了檔案。
// 寫法同 0c-update set：保留本版不認得的欄位與值。設定檔損毀或讀不到時不覆寫、回傳錯誤（修復交給設定程式），
// 來自較新版本時回 ErrReadOnly。
func Change(userStore string, pairs ...[2]string) (bool, error) {
	changed := false
	err := filelock.With(filepath.Dir(userStore), WriteLock, func() error {
		path := Path(userStore)
		r, err := Read(path)
		if err != nil {
			return err
		}
		s := r.Settings
		if s.ReadOnly() {
			return ErrReadOnly
		}
		for _, p := range pairs {
			from, _ := s.Value(p[0])
			if err := s.Set(p[0], p[1]); err != nil {
				return err
			}
			to, _ := s.Value(p[0])
			changed = changed || from != to
		}
		if !changed { // 例如 0c-update set 剛寫好、再請引擎套用：不必重寫
			return nil
		}
		return Save(path, s)
	})
	return changed, err
}
