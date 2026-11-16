package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/0c-biggo/0c-ime/core/engine"
	"github.com/0c-biggo/0c-ime/internal/datadir"
)

// errResetLocked：另一個引擎持有個人資料鎖，這個引擎寫不了 user.json，清了也存不下來。
var errResetLocked = errors.New("另一個輸入法引擎正在使用個人資料，無法清除學習紀錄；請登出再登入後再試")

// personalReset 處理設定程式的「清除學習紀錄」（#301，IPC 的 reset_personal）：在存檔鎖內清空引擎的個人資料、
// 立刻寫回 user.json。跟定時存檔用同一把鎖，清除前開始的那次存檔不會在之後把舊資料蓋回去；交接與結束時
// 存的也已經是清空後的資料。較新格式沒讀入的個人檔（personal_store=newer_format_not_loaded）同樣清成空白，
// 之後照常學習。寫檔失敗時記憶體已經清空，標記要盡快重試，並把錯誤交給呼叫端。
type personalReset struct {
	mu       *sync.Mutex // 存檔鎖（main 的 saveMu）
	eng      *engine.Engine
	path     string
	writable bool
	state    *string // personal_store：loaded 或 newer_format_not_loaded
	schedule *saveSchedule
}

func (r personalReset) run() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.writable {
		return errResetLocked
	}
	r.eng.ResetPersonal()
	*r.state = "loaded"
	r.schedule.take() // 清除前未存的學習一起作廢
	if err := datadir.SaveUser(r.eng, r.path); err != nil {
		r.schedule.changed(time.Now(), true)
		return fmt.Errorf("已清除目前的學習紀錄，但寫入 %s 失敗：%w", r.path, err)
	}
	os.Remove(r.path + ".tmp") // 以前中斷的存檔留下的暫存檔
	return nil
}
