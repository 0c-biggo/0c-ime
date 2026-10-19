//go:build !windows

package settingsapp

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"git.biggo.com/0c/ime/internal/update"
)

const (
	MacLauncherName       = "靈犀注音設定（0c）.app"
	MacLauncherID         = "com.biggo.0c.settings-launcher"
	MacLauncherExecutable = "0c-settings-launcher"
)

// checkEntryPath 不跟隨 Applications 與目的 App 內的連結，也不接管別人可寫的入口。
func checkEntryPath(path string, directory bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.IsDir() != directory || (!directory && !st.Mode().IsRegular()) || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s：不是預期的一般檔案或目錄", path)
	}
	if !ownedByCurrentUser(st) || st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s：不屬於目前使用者或其他人可以修改", path)
	}
	return nil
}

// launcherOwned 以簽章涵蓋的 bundle ID 和管理標記識別自己的入口；不碰同名的其他 App。
func launcherOwned(app string) (bool, error) {
	for _, p := range []string{app, filepath.Join(app, "Contents")} {
		if err := checkEntryPath(p, true); err != nil {
			return false, err
		}
	}
	info := filepath.Join(app, "Contents/Info.plist")
	if err := checkEntryPath(info, false); err != nil {
		return false, err
	}
	f, err := os.Open(info)
	if err != nil {
		return false, err
	}
	defer f.Close()
	d := xml.NewDecoder(io.LimitReader(f, 64*1024))
	key, id, managed := "", "", false
	for {
		t, err := d.Token()
		if errors.Is(err, io.EOF) {
			return id == MacLauncherID && managed, nil
		}
		if err != nil {
			return false, err
		}
		s, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch s.Name.Local {
		case "key":
			if err := d.DecodeElement(&key, &s); err != nil {
				return false, err
			}
		case "string":
			var value string
			if err := d.DecodeElement(&value, &s); err != nil {
				return false, err
			}
			if key == "CFBundleIdentifier" {
				id = value
			}
			key = ""
		case "true":
			if key == "0cManagedSettingsLauncher" {
				managed = true
			}
			key = ""
		}
	}
}

// withLauncherLock 序列化安裝器、背景更新與選單的入口修復。鎖檔不在 App 裡，不影響簽章。
func withLauncherLock(applications string, create bool, action func(string) (bool, error)) (bool, error) {
	if create {
		if err := os.MkdirAll(applications, 0o755); err != nil {
			return false, err
		}
	}
	if err := checkEntryPath(applications, true); err != nil {
		if !create && errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	lock := filepath.Join(applications, ".0c-settings.lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := checkEntryPath(lock, false); err != nil {
		return false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return false, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return action(filepath.Join(applications, MacLauncherName))
}

// EnsureMacLauncher 複製收據核對過的簽署 App，連簽章及公證票據都保持原樣。
// 內容相同不重寫；先完成暫存副本並核對，再改名換新。外面的 App 是可重建入口，真正的設定程式仍在輸入法裡。
func EnsureMacLauncher(applications, source string, files map[string]string) (bool, error) {
	if err := VerifyBundle(source, files); err != nil {
		return false, err
	}
	owned, err := launcherOwned(source)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, errors.New("不是靈犀注音設定啟動器")
	}
	program := filepath.Join(source, "Contents/MacOS", MacLauncherExecutable)
	if err := Verify(program, files["Contents/MacOS/"+MacLauncherExecutable]); err != nil {
		return false, err
	}
	return withLauncherLock(applications, true, func(dest string) (bool, error) {
		if _, err := os.Lstat(dest); err == nil {
			owned, err := launcherOwned(dest)
			if err != nil {
				return false, err
			}
			if !owned {
				return false, fmt.Errorf("%s 不是本程式建立的，不覆寫", dest)
			}
			if VerifyBundle(dest, files) == nil {
				return false, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		tmp, err := os.MkdirTemp(applications, ".0c-settings-")
		if err != nil {
			return false, err
		}
		cleanup := true
		defer func() {
			if cleanup {
				_ = os.RemoveAll(tmp)
			}
		}()
		stage := filepath.Join(tmp, "new.app")
		if err := os.Mkdir(stage, 0o755); err != nil {
			return false, err
		}
		for name := range files {
			// VerifyBundle 已核對清單和實際樹；清單中的檔名須仍限制在 bundle 內。
			if !filepath.IsLocal(filepath.FromSlash(name)) {
				return false, fmt.Errorf("不安全的入口路徑：%s", name)
			}
			from, to := filepath.Join(source, filepath.FromSlash(name)), filepath.Join(stage, filepath.FromSlash(name))
			if err := copyLauncherFile(from, to); err != nil {
				return false, err
			}
		}
		if err := VerifyBundle(stage, files); err != nil {
			return false, err
		}
		old := filepath.Join(tmp, "old.app")
		if err := os.Rename(dest, old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if err := os.Rename(stage, dest); err != nil {
			if restore := os.Rename(old, dest); restore != nil && !errors.Is(restore, os.ErrNotExist) {
				cleanup = false // 舊入口保留在 old，不能讓 defer 刪掉它。
				return false, errors.Join(err, fmt.Errorf("入口回復失敗，舊入口保留在 %s：%w", old, restore))
			}
			return false, err
		}
		return true, nil
	})
}

func copyLauncherFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if err := out.Chmod(st.Mode().Perm()); err != nil {
		_ = out.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

// EnsureMacUpdater 原子換上收據核對過的穩定更新器，與背景排程是否啟用無關。
// 每次使用獨立暫存檔，選單與背景修復同時執行也不會互相刪掉尚未寫完的檔案。
func EnsureMacUpdater(source, want, stable string) error {
	if err := Verify(source, want); err != nil {
		return err
	}
	parent := filepath.Dir(stable)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := checkEntryPath(parent, true); err != nil {
		return err
	}
	if err := checkEntryPath(stable, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if Verify(stable, want) == nil {
		return nil
	}
	tmp, err := os.CreateTemp(parent, ".0c-settings-updater-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	in, err := os.Open(source)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	_, copyErr := io.Copy(tmp, in)
	err = errors.Join(copyErr, in.Close(), tmp.Chmod(0o700), tmp.Close())
	if err != nil {
		return err
	}
	if sum, err := update.Hash(tmp.Name()); err != nil || sum != want {
		return errors.New("穩定更新器的暫存副本與收據不符")
	}
	return os.Rename(tmp.Name(), stable)
}

// RemoveMacLauncher 只移除帶自己 bundle ID 與標記的入口。同名的其他 App 不動。
func RemoveMacLauncher(applications string) error {
	_, err := withLauncherLock(applications, false, func(dest string) (bool, error) {
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		owned, err := launcherOwned(dest)
		if err != nil {
			return false, err
		}
		if !owned {
			return false, nil
		}
		return true, os.RemoveAll(dest)
	})
	return err
}
