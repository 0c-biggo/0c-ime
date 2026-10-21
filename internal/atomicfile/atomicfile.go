// Package atomicfile 以「同目錄暫存檔、寫完 Sync、再改名」取代整個檔案：讀的人只會讀到舊檔或完整的新檔，
// 斷電後也不會留下長度對了、內容卻是零的檔案。Windows 的 os.Rename 以 MoveFileEx(MOVEFILE_REPLACE_EXISTING)
// 覆蓋既有的檔案（目的檔被別的程式開著時失敗，呼叫端照舊回報錯誤）。暫存檔的權限是 0600。
package atomicfile

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Write 原子地把 data 寫到 path；失敗時刪掉暫存檔，原本的檔案不動。
func Write(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// WriteJSON 以兩格縮排、結尾換行的 JSON 原子寫入。
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return Write(path, append(b, '\n'))
}
