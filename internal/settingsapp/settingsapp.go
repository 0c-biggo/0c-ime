// Package settingsapp 找出、核對並啟動「靈犀注音設定」程式（docs/desktop-manager.md、#47）。
//
// 設定程式放在目前的核心版本目錄（隨核心更新，不需要管理員權限），完整安裝另外留一份在安裝位置。
// 入口一律是 0c-update open-settings：每次從 engine.json 找出目前版本，啟動前再核對檔案，
// 所以捷徑與外殼都不必指向某個版本的路徑。
package settingsapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// hiddenPages 是可以開、但沒列進用法的頁面：0.2.0 的「我的詞彙」（#125），以及 0.2.5 拆成三頁前的「輸入設定」
// （input，#207；照樣傳給設定程式，由它導到「中英切換」，舊版設定程式照舊開輸入設定）。舊版設定程式不認得時開總覽。
var hiddenPages = []string{"vocab", "input"}

// Pages 是可以指定開啟的頁面；不認得的名稱開總覽。mode、candidates、keyboard 是「中英切換」「選字」「鍵盤與符號」（#207）。
var Pages = []string{"overview", "styles", "mode", "candidates", "keyboard", "output", "update", "troubleshoot", "about", "models"}

// Page 回傳白名單內的頁面名稱。
func Page(name string) string {
	for _, p := range append(Pages, hiddenPages...) {
		if p == name {
			return p
		}
	}
	return Pages[0]
}

// 來源。
const (
	SourceCore    = "core"    // 目前核心版本目錄
	SourceInstall = "install" // 完整安裝的副本
)

// ErrNotFound：這個安裝沒有設定程式（舊版套件，或這個平台還沒出貨）。
var ErrNotFound = errors.New("這個安裝沒有附設定程式")

type Location struct {
	Path   string
	Source string
}

// Locate 找出可以啟動的設定程式：先看目前核心版本目錄（核心更新帶來的較新版本），沒有或核對不過
// 再用完整安裝的副本。installed 與 installedSHA256 是安裝位置與安裝收據記錄的雜湊（可為空）。
// 找到的檔案一定通過 Verify；找不到時回傳 ErrNotFound，找到但核對不過時回傳原因。
func Locate(base, installed, installedSHA256 string) (Location, error) {
	var problems []error
	if dir, ok := enginedir.Dir(base); ok {
		path := filepath.Join(dir, enginedir.SettingsExecutable())
		if _, err := os.Lstat(path); err == nil {
			hashes, err := enginedir.ReadFiles(dir)
			if err == nil {
				err = Verify(path, hashes[enginedir.SettingsExecutable()])
			}
			if err == nil {
				return Location{Path: path, Source: SourceCore}, nil
			}
			problems = append(problems, fmt.Errorf("核心版本目錄的設定程式：%w", err))
		}
	}
	if installed != "" {
		if _, err := os.Lstat(installed); err == nil {
			if err := Verify(installed, installedSHA256); err != nil {
				problems = append(problems, fmt.Errorf("安裝位置的設定程式：%w", err))
			} else {
				return Location{Path: installed, Source: SourceInstall}, nil
			}
		}
	}
	if len(problems) > 0 {
		return Location{}, errors.Join(problems...)
	}
	return Location{}, ErrNotFound
}

// Verify 確認檔案是目前使用者擁有的一般可執行檔、其他人不能改，且雜湊與記錄相符。
// 沒有記錄的雜湊就無從核對，視為不可啟動。
func Verify(path, want string) error {
	if want == "" {
		return errors.New("沒有可核對的雜湊記錄")
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("不是一般檔案")
	}
	if runtime.GOOS != "windows" {
		if st.Mode().Perm()&0o111 == 0 {
			return errors.New("沒有執行權限")
		}
		if st.Mode().Perm()&0o022 != 0 {
			return errors.New("群組或其他人可以修改")
		}
		if !ownedByCurrentUser(st) {
			return errors.New("不屬於目前使用者")
		}
	}
	got, err := update.Hash(path)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("雜湊與記錄不符")
	}
	return nil
}

// SourceSystem：系統套件安裝的設定程式（#181，Linux 的 /usr/bin/0c-settings）。
const SourceSystem = "system"

// LocateSystem 核對系統套件安裝的設定程式：沒有安裝收據可比對雜湊（檔案由套件管理員核對），改成確認是
// root 擁有、其他人不能改的一般可執行檔（不接受符號連結）。不存在時回傳 ErrNotFound。
func LocateSystem(path string) (Location, error) {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Location{}, ErrNotFound
		}
		return Location{}, err
	}
	switch {
	case !st.Mode().IsRegular():
		return Location{}, errors.New("系統的設定程式不是一般檔案")
	case st.Mode().Perm()&0o111 == 0:
		return Location{}, errors.New("系統的設定程式沒有執行權限")
	case st.Mode().Perm()&0o022 != 0:
		return Location{}, errors.New("系統的設定程式群組或其他人可以修改")
	case !ownedBy(st, systemOwner):
		return Location{}, errors.New("系統的設定程式不屬於 root")
	}
	return Location{Path: path, Source: SourceSystem}, nil
}
