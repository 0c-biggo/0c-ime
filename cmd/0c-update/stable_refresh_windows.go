package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// noActiveInstall：這個帳號沒有啟用中的完整安裝。收據已停用（解除安裝之後，版本檔案與個人詞庫保留、可再回復），
// 或根本沒有收據（自動更新從 0.1.0-dev.18 起才有，那時就有收據）。讀不到收據的其他錯誤不算，照常處理。
func noActiveInstall() bool {
	b, err := os.ReadFile(receiptPath())
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	var r struct {
		Active *bool `json:"active"`
	}
	return json.Unmarshal(b, &r) == nil && r.Active != nil && !*r.Active
}

// retireOrphanSchedule：背景排程的 auto 發現這個帳號已經沒有啟用中的安裝（解除安裝之後排程被重建、或解除安裝沒做完），
// 就移除自己的自動更新與登入排程後結束，不再每 15 分鐘查詢、下載新版並通知「需要確認安裝」（#284）。
// 0.2.11 以前提權建立的排程本人只能讀，刪不掉：照實回報、不嘗試，仍然結束（不查詢、不通知）。
func retireOrphanSchedule() bool {
	if !noActiveInstall() {
		return false
	}
	if err := removeSchedule(); err != nil {
		fmt.Fprintln(os.Stderr, "0c-update:", err)
	}
	switch removed, err := deleteTask(engineTaskName(), currentSID()); {
	case err != nil:
		fmt.Fprintln(os.Stderr, "0c-update: 登入啟動引擎的排程沒有移除：", err)
	case removed:
		fmt.Println("已移除登入啟動引擎的排程")
	}
	removeSettingsEntry()
	return true
}

// refreshStableUpdater：schedule repair 的一部分，見 refreshStaleUpdater。換新時以目前版本重建排程定義、
// 換上穩定副本並補上開始功能表的「靈犀注音設定」：舊副本建立的這些都是舊版內容。
func refreshStableUpdater(base string) (bool, error) {
	source, want := currentUpdater(base, receiptPath(), updaterName())
	version, _ := (&system{base: base}).Installed()
	if have, err := update.Hash(stableUpdater(base)); err == nil && stableIsNewerAsset(base, have, version) {
		return false, nil // 只換更新器裝上的較新更新器（#295）：不換回目前版本附的舊版
	}
	return refreshStaleUpdater(source, want, stableUpdater(base), func(source string) error {
		if elevatedOK() {
			if err := installScheduleFrom(base, source, version); err != nil {
				return err
			}
		} else {
			// repair 由一般權限的 0c-ui 執行；0.2.11 以前提權建立的排程一般權限覆寫會被拒絕（Access is denied），
			// 把已經換好的穩定副本也算成失敗。排程定義指向穩定副本的路徑，換檔後不必重建；定義有變時由 repair 的
			// refreshTaskDefinitions 在本人改得了時換新（這一版起提權建立的排程開放給本人），否則等下一次完整安裝。
			if err := adoptStableUpdater(source, version, base); err != nil { // 先自我測試（#293）
				return err
			}
		}
		ensureSettingsEntry()
		return nil
	})
}
