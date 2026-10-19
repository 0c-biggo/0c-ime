// Package userlock 是個人資料目錄的寫入者鎖（#120）：同一個目錄同時只有一個引擎能寫 user.json。
//
// 單一實例原本只靠 IPC 端點（連得上就退出、監聽失敗也退出），端點不同（不同參數叫起的引擎、
// 執行期目錄不同）時兩個引擎會各自存檔，後存的蓋掉先存的。這把鎖是第二道防線：拿不到鎖的引擎
// 照常輸入但不寫入。鎖由作業系統在行程結束（含當掉）時自動放開，不會留下過期的鎖。
//
// Linux／macOS 用 flock，Windows 用 LockFileEx；鎖檔 FileName 放在個人資料目錄、不刪除
// （刪除會讓正在等待的人鎖到已刪除的檔案），內容是持有者的 pid，只供紀錄。
package userlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileName 是鎖檔名稱，位於個人資料目錄（user.json 所在目錄）。
const FileName = "engine.lock"

// ErrBusy 表示另一個行程持有這個目錄的鎖。
var ErrBusy = errors.New("個人資料目錄已由另一個引擎持有")

// Lock 是持有中的鎖；Release 或行程結束時放開。
type Lock struct {
	mu sync.Mutex
	f  *os.File
}

// TryLock 嘗試立即取得 dir 的鎖，不等待。另一個行程持有時回傳 ErrBusy；
// 其他錯誤（目錄不存在、檔案系統不支援鎖）回傳原因，呼叫端自行決定是否照常寫入。
func TryLock(dir string) (*Lock, error) {
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("開啟鎖檔 %s：%w", path, err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, ErrBusy) {
			return nil, err
		}
		return nil, fmt.Errorf("鎖定 %s：%w", path, err)
	}
	// 記下持有者供另一個引擎的紀錄使用；寫不進去不影響鎖本身。
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	}
	return &Lock{f: f}, nil
}

// Release 放開鎖；可重複呼叫。鎖檔保留。
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	if closeErr := l.f.Close(); err == nil {
		err = closeErr
	}
	l.f = nil
	return err
}

// Owner 回傳鎖檔記錄的持有者 pid（盡力而為，讀不到時為空字串）；只供紀錄，不代表鎖仍被持有。
func Owner(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
