// Package filelock 是 base/update 下的命名鎖：以 O_EXCL 建立檔案、寫入持有者的 pid，結束時刪除。
// 前一次執行被中止留下的鎖，記錄的行程已不存在（或鎖超過一小時）時接手。
//
// 0c-update（更新協調器、設定寫入）與 0c-server（從輸入法選單切換「輸出簡體字」時寫設定，#169）共用這套規則，
// 同一個名稱的鎖在兩個程式之間互斥。
package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked：另一個程序正握著同一把鎖，短時間內拿不到。
var ErrLocked = errors.New("另一個程序正在修改，請稍後再試")

// Acquire 立即嘗試取得 base/update/name；被別人持有時回傳包著 ErrLocked 的錯誤。
func Acquire(base, name string) (func(), error) {
	dir := filepath.Join(base, "update")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, name)
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// 前一次執行被中止時會留下鎖：記錄的行程已不存在（或鎖超過一小時）即接手。
		if st, e := os.Stat(file); e == nil {
			var pid int
			b, e := os.ReadFile(file)
			if e != nil {
				if errors.Is(e, os.ErrNotExist) {
					return Acquire(base, name)
				}
				return nil, e
			}
			fmt.Sscan(string(b), &pid)
			if (pid > 0 && pid != os.Getpid() && !ProcessAlive(pid)) || time.Since(st.ModTime()) > time.Hour {
				if e := os.Remove(file); e != nil && !errors.Is(e, os.ErrNotExist) {
					return nil, e
				}
				return Acquire(base, name)
			}
		} else if errors.Is(e, os.ErrNotExist) {
			return Acquire(base, name)
		} else {
			return nil, e
		}
		return nil, fmt.Errorf("%s: %w", name, ErrLocked)
	}
	_, writeErr := fmt.Fprintln(f, os.Getpid())
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(file)
		return nil, err
	}
	return func() { os.Remove(file) }, nil
}

// Wait 只在被別人持有時等待，最多 2 秒；檔案系統錯誤照原樣回傳。
func Wait(ctx context.Context, base, name string) (func(), error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unlock, err := Acquire(base, name)
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, ErrLocked) || !time.Now().Before(deadline) {
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// With 取得鎖後執行 fn，最多等 2 秒。
func With(base, name string, fn func() error) error {
	unlock, err := Wait(context.Background(), base, name)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}
