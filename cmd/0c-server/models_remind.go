package main

import (
	"sync/atomic"
	"time"

	"github.com/0c-biggo/0c-ime/internal/models"
)

// modelRemind：沒有找到神經模型時（Linux、沒有內建、個人目錄也沒有這份字典的配對，#180），引擎在啟動時、
// 之後送出字而且沒有組字時最多每小時檢查一次，時候到了才叫 0c-update models remind（它在升級換字典後於本機
// 重新配對，或依排程送「下載模型」通知）。引擎這端只讀 remind.json 與一個目錄，不做網路或檔案改寫。
type modelRemind struct {
	userDir, dictionary string
	run                 func(args ...string) // 啟動 0c-update，不等它結束
	last                time.Time            // 只在存檔 goroutine 讀寫
	committed           atomic.Bool          // 上次檢查之後送出過字
}

// startup 是引擎啟動時的檢查（不算送出字；送出過字的紀錄由 0c-update 保存在 remind.json）。
func (r *modelRemind) startup(now time.Time) {
	if r == nil {
		return
	}
	r.last = now
	r.check(now, false)
}

// markCommitted 在送出字時呼叫（IPC 的 OnCommit）。
func (r *modelRemind) markCommitted() {
	if r != nil {
		r.committed.Store(true)
	}
}

// tick 由每秒的存檔 goroutine 呼叫：送出過字、沒有組字、距離上次檢查滿一小時才檢查。
func (r *modelRemind) tick(now time.Time, composing int) {
	if r == nil || composing > 0 || !r.committed.Load() || now.Sub(r.last) < models.EngineCheckEvery {
		return
	}
	r.committed.Store(false)
	r.last = now
	r.check(now, true)
}

func (r *modelRemind) check(now time.Time, committed bool) {
	if !models.EngineShouldCheck(r.userDir, r.dictionary, now) {
		return
	}
	args := []string{"models", "remind"}
	if committed {
		args = append(args, "-committed")
	}
	r.run(args...)
}
