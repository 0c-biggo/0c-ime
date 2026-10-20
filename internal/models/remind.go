package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// 沒有模型時的提醒（#180）：最多 3 次、間隔 24 小時，第一次在安裝滿一小時而且送出過字之後。
const (
	RemindMax          = 3
	RemindAfterInstall = time.Hour
	RemindEvery        = 24 * time.Hour
	// EngineCheckEvery 是引擎在送出字之後檢查的最短間隔。
	EngineCheckEvery = time.Hour
)

// 永久停止提醒的原因（remind.json 的 stopped）。
const (
	StopDownloaded  = "downloaded"  // 下載過模型
	StopInstalled   = "installed"   // 已經有模型（安裝包內建、發行版另外裝的）
	StopDismissed   = "dismissed"   // 按了「不再提醒」
	StopStatistical = "statistical" // 選字方式改成統計
	StopDone        = "done"        // 3 次送完
)

const remindFile = "remind.json"

// Remind 是 remind.json。
type Remind struct {
	Schema    int       `json:"schema"`
	FirstSeen time.Time `json:"first_seen,omitzero"` // 第一次檢查的時間（當成安裝時間）
	Committed bool      `json:"committed"`           // 送出過字
	Count     int       `json:"count"`
	Last      time.Time `json:"last,omitzero"`
	// NextAt 是下一次值得檢查的時間；引擎在這之前不呼叫 0c-update。
	NextAt    time.Time `json:"next_at,omitzero"`
	Stopped   string    `json:"stopped,omitempty"`
	StoppedAt time.Time `json:"stopped_at,omitzero"`
	// RepairFailed 是重新配對失敗過的字典雜湊（例如發行版自己重建的字典沒有配對清單）；同一份字典不再自動重試。
	RepairFailed string `json:"repair_failed,omitempty"`
}

// RemindInput 是這一次檢查時的情況。
type RemindInput struct {
	Now        time.Time
	Committed  bool // 引擎在送出字之後呼叫
	HasModel   bool // 已經有可以用的模型（內建或個人目錄）
	Downloaded bool // 個人目錄有下載過的模型（包括對不上新字典的舊模型：只由設定頁提示重新下載）
	Neural     bool // 選字設定是神經選字
	Disabled   bool // 發行版關掉了模型下載與提醒
}

// LoadRemind 讀提醒紀錄；沒有或讀不懂時從頭開始。
func (s Store) LoadRemind() Remind {
	var r Remind
	if b, err := os.ReadFile(filepath.Join(s.Root, remindFile)); err == nil {
		if json.Unmarshal(b, &r) != nil {
			r = Remind{}
		}
	}
	r.Schema = 1
	return r
}

// SaveRemind 寫提醒紀錄。
func (s Store) SaveRemind(r Remind) error {
	r.Schema = 1
	return writeJSON(filepath.Join(s.Root, remindFile), r)
}

// Stop 永久停止提醒（已停止時保留第一次的原因）。
func (r *Remind) Stop(reason string, now time.Time) {
	if r.Stopped == "" {
		r.Stopped, r.StoppedAt = reason, now.UTC().Truncate(time.Second)
	}
}

// Decide 更新紀錄並回報這一次要不要送通知。停止條件任何一項成立就永久停止。
func (r *Remind) Decide(in RemindInput) bool {
	now := in.Now.UTC().Truncate(time.Second)
	if r.FirstSeen.IsZero() || r.FirstSeen.After(now) { // 時鐘往回調過時以現在為準
		r.FirstSeen = now
	}
	if r.Last.After(now) {
		r.Last = now
	}
	if in.Committed {
		r.Committed = true
	}
	switch {
	case r.Stopped != "":
	case in.HasModel:
		r.Stop(StopInstalled, now)
	case in.Downloaded:
		r.Stop(StopDownloaded, now)
	case !in.Neural:
		r.Stop(StopStatistical, now)
	case r.Count >= RemindMax:
		r.Stop(StopDone, now)
	}
	if r.Stopped != "" {
		r.NextAt = time.Time{}
		return false
	}
	if in.Disabled { // 不是永久停止：維護者之後打開時照常提醒
		r.NextAt = now.Add(RemindEvery)
		return false
	}
	due := r.FirstSeen.Add(RemindAfterInstall)
	if !r.Last.IsZero() && r.Last.Add(RemindEvery).After(due) {
		due = r.Last.Add(RemindEvery)
	}
	r.NextAt = due
	return r.Committed && !now.Before(due)
}

// Sent 記下送出一次；送滿 3 次就停止。
func (r *Remind) Sent(now time.Time) {
	now = now.UTC().Truncate(time.Second)
	r.Count++
	r.Last = now
	r.NextAt = now.Add(RemindEvery)
	if r.Count >= RemindMax {
		r.Stop(StopDone, now)
		r.NextAt = time.Time{}
	}
}

// NotDelivered：通知服務不在（例如沒有桌面）時不算一次，隔 24 小時再試。
func (r *Remind) NotDelivered(now time.Time) {
	r.NextAt = now.UTC().Truncate(time.Second).Add(RemindEvery)
}

// EngineShouldCheck 是引擎端便宜的檢查（只讀 remind.json 與一個目錄）：沒有找到模型時，什麼時候值得叫
// 0c-update models remind。個人目錄有權重但沒有這份字典的配對（升級換了字典）時要重新配對；否則看提醒
// 有沒有停止、時間到了沒有。
func EngineShouldCheck(userDir, dictionary string, now time.Time) bool {
	s := NewStore(userDir)
	r := s.LoadRemind()
	if entries, err := os.ReadDir(filepath.Join(s.Root, "weights")); err == nil && len(entries) > 0 && validHex(dictionary) && r.RepairFailed != dictionary {
		return true
	}
	if r.Stopped != "" {
		return false
	}
	return r.NextAt.IsZero() || !now.Before(r.NextAt)
}
