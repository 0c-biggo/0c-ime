package main

import (
	"errors"
	"log"
	"time"

	"git.biggo.com/0c/ime/internal/userlock"
)

// 個人資料寫入者鎖的狀態，health 以 personal_lock 回報。
const (
	lockHeld        = "held"        // 本引擎是寫入者
	lockBusy        = "busy"        // 另一個引擎持有：照常輸入，但不寫入個人檔
	lockUnavailable = "unavailable" // 這個系統鎖不了（例如檔案系統不支援）：照舊存檔，不因此停止學習
)

const lockPoll = 50 * time.Millisecond

// claimWriter 在載入資料包之前取得個人資料目錄的寫入者鎖（#120）。
//
// 單一實例仍以端點為準（main 先試連、監聽失敗也結束）；鎖補上端點管不到的情況：
//   - 交接：舊引擎關閉端點後還要做最後一次存檔，存完才放鎖。協調器在端點釋放後就啟動新引擎，
//     新引擎在這裡等鎖，放開後才讀個人檔，不會讀到最後一次存檔之前的內容、之後再蓋掉它。
//   - 同時啟動（登入排程與外殼的第一個按鍵）：先拿到鎖的載入資料包，另一個在這裡等，
//     一看到端點有人服務就讓位（yield），不白載資料包。
//   - 端點不同、個人檔相同（不同參數叫起的第二個引擎）：等到 wait 仍拿不到，照常輸入但不存檔。
//
// 外殼把「連得上」當成引擎已就緒（macOS 據此重送冷啟動時暫存的按鍵、Linux 外殼的請求上限 2 秒），
// 所以監聽仍在資料包載入之後；提早占住的是這把鎖。
func claimWriter(dir string, names []string, wait time.Duration, lg *log.Logger) (lock *userlock.Lock, state string, yield bool) {
	deadline := time.Now().Add(wait)
	waited := false
	for {
		l, err := userlock.TryLock(dir)
		if err == nil {
			if waited {
				lg.Printf("已取得個人資料鎖（前一個寫入者已放開）")
			}
			return l, lockHeld, false
		}
		if !errors.Is(err, userlock.ErrBusy) {
			lg.Printf("無法鎖定個人資料目錄，照舊存檔：%v", err)
			return nil, lockUnavailable, false
		}
		for _, name := range names {
			if serving(name) {
				return nil, "", true
			}
		}
		if !time.Now().Before(deadline) {
			lg.Printf("個人資料目錄 %s 由另一個引擎（pid %s）持有：本引擎照常輸入，但不存檔，以免兩個寫入者互相覆蓋", dir, userlock.Owner(dir))
			return nil, lockBusy, false
		}
		waited = true
		time.Sleep(lockPoll)
	}
}
