package main

import (
	"strings"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/models"
)

// 引擎只在啟動時、之後送出字又沒在組字時最多每小時叫一次 0c-update models remind；remind.json 說停了就不叫。
func TestModelRemindChecksAtStartupAndHourlyAfterCommits(t *testing.T) {
	dir := t.TempDir()
	var calls []string
	r := &modelRemind{userDir: dir, dictionary: strings.Repeat("a", 64), run: func(args ...string) { calls = append(calls, strings.Join(args, " ")) }}
	t0 := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	r.startup(t0)
	if strings.Join(calls, "|") != "models remind" {
		t.Fatalf("啟動時檢查一次：%v", calls)
	}
	r.tick(t0.Add(2*time.Hour), 0)
	if len(calls) != 1 {
		t.Fatal("沒送出過字不檢查")
	}
	r.markCommitted()
	r.tick(t0.Add(30*time.Minute), 0)
	if len(calls) != 1 {
		t.Fatal("距離上次不到一小時")
	}
	r.tick(t0.Add(2*time.Hour), 1)
	if len(calls) != 1 {
		t.Fatal("組字中不檢查")
	}
	r.tick(t0.Add(2*time.Hour), 0)
	if len(calls) != 2 || calls[1] != "models remind -committed" {
		t.Fatalf("%v", calls)
	}
	r.tick(t0.Add(2*time.Hour+time.Minute), 0)
	if len(calls) != 2 {
		t.Fatal("同一小時內不再檢查")
	}
	// 0c-update 記下下一次的時間：之前不叫；停止之後不叫
	s := models.NewStore(dir)
	s.SaveRemind(models.Remind{NextAt: t0.Add(10 * time.Hour)})
	r.markCommitted()
	r.tick(t0.Add(4*time.Hour), 0)
	if len(calls) != 2 {
		t.Fatal("還沒到 next_at")
	}
	rec := models.Remind{}
	rec.Stop(models.StopDismissed, t0)
	s.SaveRemind(rec)
	r.markCommitted()
	r.tick(t0.Add(30*time.Hour), 0)
	if len(calls) != 2 {
		t.Fatal("停止後不叫")
	}
	var none *modelRemind // 有模型或不是 Linux：nil，什麼都不做
	none.startup(t0)
	none.markCommitted()
	none.tick(t0, 0)
}
