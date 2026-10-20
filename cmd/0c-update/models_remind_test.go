//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0c-biggo/0c-ime/internal/models"
)

// 配對目錄在、引擎正在用，但 paired.json 是舊的 models.lock 配的（例如換新 revision 的下載在權重放好、配對還沒放上去
// 時中斷）：need=repair、source=user。以前 remind 只在 source=none 時重新配對，設定頁一直顯示「正在重新配對」
// 又沒有按鈕；現在 remind 照樣在本機重新配對，完成後請引擎交接。
func TestModelsRemindRepairsStalePairInUse(t *testing.T) {
	f := newModelsFixture(t)
	if err := f.env.download(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(f.env.store().PairDir(f.env.dictionary()), "paired.json")
	var info map[string]any
	b, _ := os.ReadFile(record)
	json.Unmarshal(b, &info)
	info["models_lock_sha256"] = "0000000000000000000000000000000000000000000000000000000000000000"
	b, _ = json.Marshal(info)
	os.WriteFile(record, b, 0o644)
	if st := f.status(t); st.Need != "repair" || st.Source != "user" {
		t.Fatalf("%+v", st)
	}
	// 引擎載入了舊的配對：它也要叫 remind（狀態跟 0c-update 上次處理的不同）
	if !models.EngineShouldCheck(f.env.base, f.env.dictionary(), time.Now()) {
		t.Fatal("引擎應叫 remind 重新配對")
	}
	hits, reloads := *f.hits, f.reloads
	if err := f.env.remind(false); err != nil {
		t.Fatal(err)
	}
	if st := f.status(t); st.Need != "none" || st.Source != "user" || *f.hits != hits || f.reloads != reloads+1 || f.notices != 0 {
		t.Fatalf("%+v hits %d→%d reloads %d→%d notices %d", st, hits, *f.hits, reloads, f.reloads, f.notices)
	}
	if models.EngineShouldCheck(f.env.base, f.env.dictionary(), time.Now().Add(1000*time.Hour)) {
		t.Fatal("重新配對之後引擎不必再叫")
	}
}

// 0c-update 看到的情況不需要處理、引擎卻找不到模型（例如 worker 腳本不在）：以前 EngineShouldCheck 只看權重目錄
// 有沒有東西，引擎每次啟動、每小時都白叫一次 0c-update。現在 remind 記下處理過的狀態，狀態沒變就不再叫。
func TestModelsRemindRecordsCheckedState(t *testing.T) {
	f := newModelsFixture(t)
	if err := f.env.download(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	dict := f.env.dictionary()
	if models.EngineShouldCheck(f.env.base, dict, time.Now()) {
		t.Fatal("下載完成時已記下狀態，交接後的新引擎不必再叫")
	}
	os.Remove(filepath.Join(filepath.Dir(f.env.data), "neural", "sequence_runtime.py"))
	f.env.store().SaveRemind(models.Remind{Stopped: models.StopDownloaded}) // 例如舊的紀錄：沒有處理過的狀態
	if !models.EngineShouldCheck(f.env.base, dict, time.Now()) {
		t.Fatal("還沒處理過這個狀態：叫一次")
	}
	if err := f.env.remind(true); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Duration{0, time.Hour, 100 * time.Hour} {
		if models.EngineShouldCheck(f.env.base, dict, time.Now().Add(at)) {
			t.Fatalf("處理過、狀態沒變，%v 後不應再叫", at)
		}
	}
	// 狀態變了（例如刪掉後又下載了別的權重）：再叫一次
	os.MkdirAll(filepath.Join(f.env.base, "models", "weights", "select", "1234"), 0o700)
	if !models.EngineShouldCheck(f.env.base, dict, time.Now()) {
		t.Fatal("狀態變了要再叫")
	}
}
