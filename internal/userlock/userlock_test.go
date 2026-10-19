package userlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 同一個個人資料目錄只能有一個持有者；放開後下一個才拿得到，持有者的 pid 記在鎖檔裡供紀錄。
func TestOnlyOneHolderPerDirectory(t *testing.T) {
	dir := t.TempDir()
	first, err := TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("第二個持有者應得到 ErrBusy：%v", err)
	}
	if owner := Owner(dir); owner != fmt.Sprint(os.Getpid()) {
		t.Fatalf("鎖檔應記錄持有者 pid：%q", owner)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := TryLock(dir)
	if err != nil {
		t.Fatalf("放開後應拿得到：%v", err)
	}
	defer second.Release()
	// 別的目錄互不影響。
	other, err := TryLock(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other.Release()
}

// 鎖檔留在原地（刪除鎖檔會讓等待中的人鎖到已刪除的檔案）；Release 可重複呼叫。
func TestReleaseKeepsLockFileAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := TryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("重複放開不應出錯：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("鎖檔應保留：%v", err)
	}
}

// 目錄不存在或不能建立鎖檔不是 ErrBusy：呼叫端據此區分「另一個引擎在寫」與「這個系統鎖不了」。
func TestMissingDirectoryIsNotBusy(t *testing.T) {
	_, err := TryLock(filepath.Join(t.TempDir(), "missing"))
	if err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("應回報非 ErrBusy 的錯誤：%v", err)
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("錯誤應指出鎖檔：%v", err)
	}
}
