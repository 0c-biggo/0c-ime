package main

import (
	"errors"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/settingsapp"
)

// Windows 開始功能表「靈犀注音設定（0c）」的維護規則。平台動作以 hook 傳入，規則本身在各平台都能測試。
//
// #284：捷徑原本是 conhost.exe --headless "<0c-update.exe>" open-settings，被 Symantec Endpoint Protection 當成惡意
// LNK（Scr.Malcode!gen）隔離；每 15 分鐘的自動更新排程又看到捷徑不見就重建，通知一直跳出來。現在：
//   - 捷徑直接指向設定程式（GUI 程式）在 bin\ 的穩定副本，路徑不隨版本變動，不經 conhost、不帶參數；
//   - 我們建過、之後被使用者或防毒軟體移除的捷徑，只有完整安裝（restore）才補回；
//   - 收據已停用（解除安裝之後）一律移除，不因 engine.json 仍指向核心目錄而重建。

// stableSettings 是設定程式的穩定副本（與穩定更新器、0c-mcp 同一個目錄），開始功能表捷徑指向這裡。
func stableSettings(base string) string {
	return filepath.Join(base, "bin", enginedir.SettingsExecutable())
}

type startMenuEntry struct {
	base    string
	restore bool // 完整安裝：使用者移除過的捷徑也補回
	// installed 是完整安裝收據的版本；收據已停用（解除安裝）時為空字串。
	installed func() (string, error)
	// locate 是目前版本、已核對的設定程式與它的雜湊；沒有設定程式時回傳 settingsapp.ErrNotFound。
	locate func() (source, sha256 string, err error)
	spec   func(stable string) (settingsapp.ShortcutSpec, error)
	ensure func(spec settingsapp.ShortcutSpec, restore bool) (bool, error)
}

func (e startMenuEntry) sync() {
	version, err := e.installed()
	if err != nil {
		return
	}
	if version == "" {
		e.remove()
		return
	}
	source, want, err := e.locate()
	if errors.Is(err, settingsapp.ErrNotFound) {
		e.remove()
		return
	}
	if err != nil {
		return // 設定程式被改過：捷徑與穩定副本照舊，open-settings 會說明無法通過核對
	}
	if !regularFile(stableUpdater(e.base)) {
		return // 設定程式靠穩定更新器運作；它不見了由 schedule repair 處理
	}
	stable := stableSettings(e.base)
	spec, err := e.spec(stable)
	if err != nil {
		return
	}
	if !e.restore && settingsapp.Recorded(e.base, spec) {
		if !regularFile(spec.Path) {
			return // 我們建過、後來被移除：不重建，也不再補穩定副本
		}
		if !regularFile(stable) {
			// 捷徑還在而它指向的穩定副本不見了（防毒軟體隔離）：不留下點了會出錯的捷徑，也不一直複製回來。
			_ = settingsapp.WithdrawShortcut(e.base)
			return
		}
	}
	if _, err := syncStableMCP(source, want, stable); err != nil {
		return
	}
	_, _ = e.ensure(spec, e.restore)
}

func (e startMenuEntry) remove() {
	_ = settingsapp.RemoveShortcut(e.base)
	_, _ = retireFile(stableSettings(e.base))
}

// currentSettings 是 open-settings 會啟動的那一份設定程式（核心目錄優先，其次完整安裝的版本目錄）與它記錄的雜湊。
func currentSettings(base, receipt string) (string, string, error) {
	name := enginedir.SettingsExecutable()
	installed, hash := installedFromReceipt(receipt, base, name)
	loc, err := settingsapp.Locate(base, installed, hash)
	if err != nil {
		return "", "", err
	}
	if loc.Source == settingsapp.SourceCore {
		files, err := enginedir.ReadFiles(filepath.Dir(loc.Path))
		if err != nil {
			return "", "", err
		}
		return loc.Path, files[name], nil
	}
	return loc.Path, hash, nil
}
