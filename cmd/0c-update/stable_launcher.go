package main

import (
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// Windows 的自動更新排程、登入啟動引擎的排程與通知按鈕的 zc-update: 協定，原本以 conhost.exe --headless 執行穩定的
// 0c-update，避免閃出主控台視窗。「conhost --headless 帶起另一個程式」是端點防護直接看命令列的規則（#289，#284 的
// 後續），所以改成執行 bin\0c-updatew.exe：GUI 子系統的小程式（cmd/0c-updatew），以沒有視窗的主控台執行同目錄的
// 0c-update.exe。它只轉接，不含更新邏輯，所以不必跟 0c-update 同步換版；隨套件附上，在 bin\ 放一份穩定副本。

const launcherName = "0c-updatew.exe"

// stableLauncher 是排程與協定執行的啟動器，和穩定更新器同一個目錄（它執行的就是旁邊那一份 0c-update.exe）。
func stableLauncher(base string) string { return filepath.Join(base, "bin", launcherName) }

// syncStableLauncher 讓穩定副本 stable 與目前版本附的啟動器（source，安裝時記錄的雜湊 want）一致；被移除了也補回
// （排程指著它，少了它自動更新就停了）。和 0c-mcp 不同，目前版本沒有附（舊版套件、回復到舊版）時保留原本那一份：
// 排程與協定可能正指著它，而它只是轉接，跟哪一版的 0c-update 都能搭配。回傳有沒有變更。
func syncStableLauncher(source, want, stable string) (bool, error) {
	if source == "" || want == "" {
		return false, nil
	}
	return syncStableMCP(source, want, stable)
}

// installLauncherBeside：建立排程時，從新版更新器 updater 旁邊（剛安裝的版本目錄或核心目錄）換上啟動器；
// 那裡沒有（舊版套件）就保留現有的。內容相同時不動。updater 與穩定更新器是同一個檔案時，旁邊的就是穩定副本本身。
func installLauncherBeside(updater, stable string) error {
	source := filepath.Join(filepath.Dir(updater), launcherName)
	if !regularFile(source) {
		return nil
	}
	if have, err := update.Hash(stable); err == nil {
		if want, err := update.Hash(source); err == nil && have == want {
			removeOldCopies(stable)
			return nil
		}
	}
	return replaceUpdater(source, stable)
}
