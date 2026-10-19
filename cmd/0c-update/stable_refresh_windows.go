package main

// refreshStableUpdater：schedule repair 的一部分，見 refreshStaleUpdater。換新時以目前版本重建排程定義、
// 換上穩定副本並補上開始功能表的「靈犀注音設定」：舊副本建立的這些都是舊版內容。
func refreshStableUpdater(base string) (bool, error) {
	source, want := currentUpdater(base, receiptPath(), updaterName())
	return refreshStaleUpdater(source, want, stableUpdater(base), func(source string) error {
		if elevatedOK() {
			if err := installScheduleFrom(base, source); err != nil {
				return err
			}
		} else {
			// repair 由一般權限的 0c-ui 執行；排程是提權的安裝程式建立的，一般權限覆寫會被拒絕（Access is denied），
			// 把已經換好的穩定副本也算成失敗。排程定義指向穩定副本的路徑，換檔後不必重建；定義有變時由下一次完整安裝重建。
			if err := replaceUpdater(source, stableUpdater(base)); err != nil {
				return err
			}
		}
		ensureSettingsEntry()
		return nil
	})
}
