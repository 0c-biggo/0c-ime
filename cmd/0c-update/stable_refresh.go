package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/0c-biggo/0c-ime/internal/enginedir"
	"github.com/0c-biggo/0c-ime/internal/update"
)

// refreshStaleUpdater 讓穩定副本（排程、選單、通知與開始功能表捷徑執行的 0c-update）跟上目前版本。
//
// 完整外殼更新由舊的穩定副本自己執行（Windows 上它以 UAC 重新啟動自己），裝完後建立排程只會把「自己」複製到
// 自己身上，穩定副本就停在舊版；核心更新生效時換穩定副本的失敗也只被略過。新版才有的子命令（例如 open-settings）
// 舊副本不認得，選單與捷徑點了沒有反應。舊程式碼沒辦法修自己，所以由新版更新器在 schedule repair 時補上。
//
// 來源一律是「目前版本」的 0c-update（source），先依安裝時記錄的雜湊（want）核對，與誰執行 repair 無關：
// 重新登入之前仍在執行的舊版 0c-ui 呼叫 repair，也只會換成目前版本，不會降版。reinstall 以來源重建排程與
// 穩定副本。穩定副本不存在由 repair 的既有規則處理（那是防毒軟體隔離，要限制次數與通知）。
func refreshStaleUpdater(source, want, stable string, reinstall func(source string) error) (bool, error) {
	if source == "" || want == "" {
		return false, nil
	}
	if info, err := os.Stat(stable); err != nil || !info.Mode().IsRegular() {
		return false, nil
	}
	if have, err := update.Hash(stable); err == nil && have == want {
		return false, nil
	}
	got, err := update.Hash(source)
	if err != nil {
		return false, err
	}
	if got != want {
		return false, fmt.Errorf("目前版本的 0c-update 與安裝紀錄不符")
	}
	if err = reinstall(source); err != nil {
		return false, err
	}
	return true, nil
}

// currentUpdater 是目前版本的更新器（name）與安裝時核對過的雜湊。有引擎指標（核心更新）時取核心目錄
// core-files.json 的紀錄；核心目錄沒有記錄更新器就不換，不退回較舊的完整安裝版本。沒有指標時取完整安裝
// 收據 receipt（已停用的收據、不安全的目錄都不算）。
func currentUpdater(base, receipt, name string) (string, string) {
	if dir, ok := enginedir.Dir(base); ok {
		files, err := enginedir.ReadFiles(dir)
		if err != nil || files[name] == "" {
			return "", ""
		}
		return filepath.Join(dir, name), files[name]
	}
	return installedFromReceipt(receipt, base, name)
}
