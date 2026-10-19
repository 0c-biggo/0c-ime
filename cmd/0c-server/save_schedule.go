package main

import (
	"sync"
	"time"
)

// 個人資料存檔時機（issue #129）。存檔是整份重寫：以前提交後每 2 秒至多寫一次，連續打字一小時
// 最多寫 1,800 次。現在改成使用者停下來才寫，連續打字時也有最長間隔，交接與結束時一定寫。
// 引擎異常結束（當掉、被強制終止、斷電）時，最多遺失最後 saveMaxDelay 內的學習。
const (
	saveIdle     = 10 * time.Second // 最後一次按鍵與學習之後這麼久沒有動作就存
	saveMaxDelay = 60 * time.Second // 有未存的學習時，最晚這麼久一定存一次
	saveTick     = time.Second
)

// saveSchedule 記錄有沒有未存的學習與它們從何時開始；只做判斷，存檔由呼叫端執行。
type saveSchedule struct {
	idle, maxDelay time.Duration

	mu         sync.Mutex
	dirtySince time.Time // 零值：沒有未存的學習
	lastChange time.Time
	urgent     bool // 明確刪除學習：下一次檢查就存，不等停止打字
}

func newSaveSchedule() *saveSchedule {
	return &saveSchedule{idle: saveIdle, maxDelay: saveMaxDelay}
}

// changed 在學習改變後呼叫；urgent 表示不必等使用者停下來。
func (s *saveSchedule) changed(now time.Time, urgent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtySince.IsZero() {
		s.dirtySince = now
	}
	s.lastChange = now
	s.urgent = s.urgent || urgent
}

// due 回報現在是否該存：有未存的學習，而且（要求立即存、使用者已停下 idle、或已等了 maxDelay）。
// lastKey 是最近一次按鍵請求的時間。
func (s *saveSchedule) due(now, lastKey time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirtySince.IsZero() {
		return false
	}
	active := s.lastChange
	if lastKey.After(active) {
		active = lastKey
	}
	return s.urgent || now.Sub(active) >= s.idle || now.Sub(s.dirtySince) >= s.maxDelay
}

// take 在開始存檔前呼叫：回報有沒有未存的學習並清除標記。存檔失敗時呼叫 restore，
// 讓下一次檢查重試；存檔期間新到的學習會再標記一次，不會遺失。
func (s *saveSchedule) take() (dirty bool, restore func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since, urgent := s.dirtySince, s.urgent
	s.dirtySince, s.urgent = time.Time{}, false
	return !since.IsZero(), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.dirtySince.IsZero() || since.Before(s.dirtySince) {
			s.dirtySince = since
		}
		s.urgent = s.urgent || urgent
	}
}
