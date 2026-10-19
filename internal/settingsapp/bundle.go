package settingsapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"git.biggo.com/0c/ime/internal/update"
)

// macOS 的設定程式是輸入法 App 內的一個巢狀 App（Contents/Helpers/0c-settings.app），整個 0c.app 是一個
// 版本單位：它的每個檔案都列在安裝收據裡，隨輸入法一起安裝、備份與回復。以下是這種「目錄樹」形式的核對與啟動。

// BundleFiles 從收據的檔案表取出某個子目錄的檔案，路徑改成相對該子目錄。
func BundleFiles(files map[string]string, prefix string) map[string]string {
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	out := map[string]string{}
	for name, sum := range files {
		if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
			out[rest] = sum
		}
	}
	return out
}

// VerifyBundle 要求 dir 內的檔案正好是 files 所列，且雜湊相符；不接受連結、額外或缺少的檔案，
// 也不接受不屬於目前使用者、或群組／其他人可以修改的目錄與檔案（Windows 不檢查擁有者與權限）。
func VerifyBundle(dir string, files map[string]string) error {
	if len(files) == 0 {
		return errors.New("沒有可核對的雜湊記錄")
	}
	seen := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(dir, path)
		name := filepath.ToSlash(rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" {
			if info.Mode().Perm()&0o022 != 0 {
				return fmt.Errorf("%s：群組或其他人可以修改", name)
			}
			if !ownedByCurrentUser(info) {
				return fmt.Errorf("%s：不屬於目前使用者", name)
			}
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s：不是一般檔案", name)
		}
		want, ok := files[name]
		if !ok {
			return fmt.Errorf("%s：不在安裝記錄裡", name)
		}
		got, err := update.Hash(path)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s：雜湊與記錄不符", name)
		}
		seen++
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(files) {
		return fmt.Errorf("缺少 %d 個檔案", len(files)-seen)
	}
	return nil
}

// LocateBundle 找出安裝在 app 的設定程式（.app）並完整核對。目錄不存在、或收據沒有記錄它的檔案
// （舊版套件）時回傳 ErrNotFound；找到但核對不過時回傳原因。
func LocateBundle(app string, files map[string]string) (Location, error) {
	if len(files) == 0 {
		return Location{}, ErrNotFound
	}
	if _, err := os.Lstat(app); errors.Is(err, fs.ErrNotExist) {
		return Location{}, ErrNotFound
	}
	if err := VerifyBundle(app, files); err != nil {
		return Location{}, fmt.Errorf("設定程式：%w", err)
	}
	return Location{Path: app, Source: SourceInstall}, nil
}

// openCommand 是啟動 App 的系統工具；notifyCommand 送出 Darwin 通知（notify(3)）。測試會換成假的。
var (
	openCommand   = "/usr/bin/open"
	notifyCommand = "/usr/bin/notifyutil"
)

// NavigateNotification 是 macOS 設定程式監聽的 Darwin 通知名稱：收到就切到該頁（#125）。
// `user.uid.<uid>.` 是系統保留給該使用者的名稱空間，只有同一個使用者（或 root）能送出，別的帳號切不動這個人的視窗。
// 通知不帶資料，頁面寫在名稱裡，所以設定程式只認白名單內的幾個名稱。與 platforms/settings 的 navigate_notification 一致。
func NavigateNotification(uid int, page string) string {
	return fmt.Sprintf("user.uid.%d.com.biggo.0c.settings.page.%s", uid, Page(page))
}

// LaunchApp 以 LaunchServices 啟動 .app：已經在執行時，系統直接把它帶到前景（macOS 允許，不受 Wayland 那種
// 搶焦點限制），新啟動時 --page 才有作用。open 啟動後立刻返回；失敗時回傳 *ExitError 並附 open 的錯誤訊息。
// 已經開著時 open 的參數不會交給它，所以接著送出切頁通知（NavigateNotification）；新啟動的行程收不收得到都無妨，
// 它本來就開在 --page 指定的頁面。
func LaunchApp(app string, o LaunchOptions) error {
	wait := o.Wait
	if wait <= 0 {
		wait = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, openCommand, app, "--args", "--page", Page(o.Page))
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		postNavigate(o.Page)
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return &ExitError{Code: exit.ExitCode(), Tail: strings.TrimSpace(stderr.String())}
	}
	return err
}

// postNavigate 請已經開著的設定程式切到 page。只是方便：失敗（舊版設定程式不監聽、notifyutil 不在）時視窗
// 仍已帶到前面，只是停在原本那頁，所以不回報錯誤。
func postNavigate(page string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, notifyCommand, "-p", NavigateNotification(os.Getuid(), page)).Run()
}
