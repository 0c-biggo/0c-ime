package main

import (
	"testing"
	"time"
)

// 連續打字時不每次提交都整份重寫：停下 saveIdle 才存，持續打字也最晚 saveMaxDelay 存一次（#129）。
func TestSaveScheduleWaitsForPauseWithUpperBound(t *testing.T) {
	s := newSaveSchedule()
	t0 := time.Unix(1_800_000_000, 0)
	if s.due(t0, t0) {
		t.Fatal("沒有學習不該存檔")
	}
	// 每 3 秒提交一次、按鍵不斷：一直沒有停下來。
	saves := 0
	for at := time.Duration(0); at <= 10*time.Minute; at += time.Second {
		now := t0.Add(at)
		if at%(3*time.Second) == 0 {
			s.changed(now, false)
		}
		if s.due(now, now) {
			if dirty, _ := s.take(); !dirty {
				t.Fatal("due 但沒有未存的學習")
			}
			saves++
		}
	}
	if want := int(10 * time.Minute / saveMaxDelay); saves < want-1 || saves > want+1 {
		t.Fatalf("連續打字 10 分鐘應約每 %v 存一次（%d 次），實際 %d 次", saveMaxDelay, want, saves)
	}

	// 停下來：最後一次按鍵後 saveIdle 才存。
	s = newSaveSchedule()
	s.changed(t0, false)
	if s.due(t0.Add(saveIdle-time.Second), t0) {
		t.Fatal("停下不到 saveIdle 就存檔")
	}
	if !s.due(t0.Add(saveIdle), t0) {
		t.Fatal("停下 saveIdle 後應存檔")
	}
	// 只在組字（按鍵、還沒提交）也算還在打字。
	if s.due(t0.Add(saveIdle), t0.Add(saveIdle-time.Second)) {
		t.Fatal("仍在按鍵時不該因提交後的閒置就存檔")
	}
}

// 明確刪除學習：下一次檢查就存，不等停止打字。
func TestSaveScheduleForgetIsUrgent(t *testing.T) {
	s := newSaveSchedule()
	t0 := time.Unix(1_800_000_000, 0)
	s.changed(t0, true)
	if !s.due(t0, t0) {
		t.Fatal("刪除學習應立即存檔")
	}
	s.take()
	s.changed(t0, false)
	if s.due(t0, t0) {
		t.Fatal("存過之後一般學習不再是立即存檔")
	}
}

// 存檔失敗要保留未存標記與最早時間，下次重試；存檔期間的新學習不遺失。
func TestSaveScheduleRestoreAfterFailure(t *testing.T) {
	s := newSaveSchedule()
	t0 := time.Unix(1_800_000_000, 0)
	s.changed(t0, false)
	dirty, restore := s.take()
	if !dirty {
		t.Fatal("應有未存的學習")
	}
	if dirty, _ := s.take(); dirty {
		t.Fatal("take 之後應清除標記")
	}
	s.changed(t0.Add(30*time.Second), false) // 存檔期間又提交
	restore()
	if !s.due(t0.Add(saveMaxDelay), t0.Add(saveMaxDelay)) {
		t.Fatal("失敗後應以最早的未存時間計算最長間隔")
	}
}
