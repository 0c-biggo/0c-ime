// Package autoupdate 是自動更新協調器：由 OS 排程喚起，查詢已簽章索引、下載、準備、
// 在安全時機切換並確認生效；受阻時保存原因與起算時間並提醒（docs/automatic-updates.md、
// docs/update-architecture.md）。每次執行做一步可恢復的工作，狀態跨重啟保存。
package autoupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"git.biggo.com/0c/ime/internal/filelock"
)

// 模式：自動保持最新（推薦）、下載後詢問、手動。
const (
	ModeAuto   = "auto"
	ModeAsk    = "ask"
	ModeManual = "manual"
)

// 階段。
const (
	StageIdle       = "idle"       // 沒有待處理版本
	StageFound      = "found"      // 發現有資格的新版
	StageDownloaded = "downloaded" // 已下載並核對雜湊
	StagePrepared   = "prepared"   // 新版本目錄已準備並通過隔離檢查
	StagePending    = "pending"    // 已安裝，待重新登入等外部條件才生效
	StageActive     = "active"     // 已核對運作中的版本即為目標版本
	StageFailed     = "failed"     // 健康檢查失敗已回復，暫停該版本
)

// Target 是本次要更新到的版本與資產（取自已驗證的簽章索引）。
type Target struct {
	Version string    `json:"version"`
	Kind    string    `json:"kind"` // core 或 shell
	URL     string    `json:"url"`
	SHA256  string    `json:"sha256"`
	Size    int64     `json:"size"`
	FoundAt time.Time `json:"found_at"`
}

// Latest 不可更新的原因（Eligible 為 false 且索引版本比已安裝的新時）。
const (
	ReasonRollout   = "rollout"   // 分批發布還沒輪到這台電腦
	ReasonNoAsset   = "no_asset"  // 索引沒有這個平台的安裝包
	ReasonWithdrawn = "withdrawn" // 該版本已撤回
	ReasonFailed    = "failed"    // 這台電腦上該版本健康檢查失敗過，不再自動重試
)

// Latest 是索引上最新版本對這台電腦的資格。Reason 只在索引版本比已安裝的新、但仍不可更新時有值。
type Latest struct {
	Version  string `json:"version"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

// Blocked 是受阻原因；同一原因的起算時間不因新版本出現而歸零。
type Blocked struct {
	Reason string    `json:"reason"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since"`
}

type Event struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Version string    `json:"version,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// NoticeSnooze is a single reminder requested for one verified asset. It only
// postpones the notification, never grants consent or postpones safe updates.
type NoticeSnooze struct {
	Version string    `json:"version"`
	SHA256  string    `json:"sha256"`
	Until   time.Time `json:"until"`
}

type State struct {
	Schema      int       `json:"schema"`
	InstallID   string    `json:"install_id"` // 分批資格用的隨機識別碼；不上傳
	Mode        string    `json:"mode"`
	PausedUntil time.Time `json:"paused_until,omitempty"`
	Feed        string    `json:"feed,omitempty"` // 非預設的已簽章索引（測試用）；空值為正式索引
	// ProfileAtLogin：首次安裝時 Fcitx5 正在執行，下次登入、桌面啟動前再把輸入法加入清單（Linux）。
	ProfileAtLogin bool `json:"profile_at_login,omitempty"`
	// ScheduleRemoved：使用者以 schedule remove 關閉排程；此時不自動修復排程。
	ScheduleRemoved bool `json:"schedule_removed,omitempty"`
	// UpdaterRepairs：自動重建排程與穩定更新器的時間（防毒軟體移除後）；UpdaterBroken：重建後又被移除，
	// 不再自動重建，等使用者處理（例如回報誤判、更新防護定義）；UpdaterNotified：已通知使用者的時間。
	UpdaterRepairs  []time.Time `json:"updater_repairs,omitempty"`
	UpdaterBroken   string      `json:"updater_broken,omitempty"`
	UpdaterNotified time.Time   `json:"updater_notified,omitempty"`
	// Test 是 E2E 的縮短週期；只有 -tags e2etest 建置會採用（報告須標示）。
	Test *TestTiming `json:"test,omitempty"`

	LastCheck     time.Time `json:"last_check,omitempty"`
	LastSuccess   time.Time `json:"last_success,omitempty"`
	NextCheck     time.Time `json:"next_check,omitempty"`
	CheckFailures int       `json:"check_failures,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	FeedSerial    uint64    `json:"feed_serial,omitempty"`
	// Latest 是最近一次成功查詢時索引上的最新版本，以及它對這台電腦是否可更新。
	// 介面靠它分辨「已是最新」與「有新版但還輪不到／沒有這個平台的安裝包」。
	Latest *Latest `json:"latest,omitempty"`

	Stage      string    `json:"stage"`
	Target     *Target   `json:"target,omitempty"`
	Download   string    `json:"download,omitempty"` // 已核對的壓縮包
	Prepared   string    `json:"prepared,omitempty"` // 準備好的版本目錄（相對個人資料目錄）
	PreparedAt time.Time `json:"prepared_at,omitempty"`
	Previous   string    `json:"previous_engine,omitempty"` // 舊版（dev.21 以前）的欄位；原指標改由平台記在指標檔內
	Switched   bool      `json:"switched,omitempty"`        // 正在切換或已切換、尚未確認；中止後重跑時據此收尾
	// RestartPending：已回復指標，但未通過檢查的引擎仍在組字，等交接後啟動原版。
	RestartPending bool     `json:"restart_pending,omitempty"`
	Blocked        *Blocked `json:"blocked,omitempty"`
	Failed         []string `json:"failed,omitempty"` // 健康檢查失敗、不再自動重試的版本

	LastNotice  time.Time     `json:"last_notice,omitempty"`
	NoticeCount int           `json:"notice_count,omitempty"`
	NoticeFor   string        `json:"notice_for,omitempty"` // 目前提醒對應的「版本／原因」
	Snooze      *NoticeSnooze `json:"notice_snooze,omitempty"`
	History     []Event       `json:"history,omitempty"`
}

type TestTiming struct {
	CheckEvery  Duration `json:"check_every,omitempty"`
	IdleNeeded  Duration `json:"idle_needed,omitempty"`
	RemindAfter Duration `json:"remind_after,omitempty"`
}

// Duration 以 "90s" 這類字串保存。
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Duration(v)
	return err
}

const maxHistory = 50

func (s *State) record(now time.Time, kind, version, detail string) {
	s.History = append(s.History, Event{At: now, Kind: kind, Version: version, Detail: detail})
	if len(s.History) > maxHistory {
		s.History = s.History[len(s.History)-maxHistory:]
	}
}

// Record 記錄一筆事件到歷程（協調器以外的動作，例如重建排程）。
func (s *State) Record(kind, detail string) { s.record(time.Now(), kind, "", detail) }

// block 記錄受阻原因；原因相同時保留原本的起算時間。
func (s *State) block(now time.Time, reason, detail string) {
	if s.Blocked != nil && s.Blocked.Reason == reason {
		s.Blocked.Detail = detail
		return
	}
	s.Blocked = &Blocked{Reason: reason, Detail: detail, Since: now}
}

func (s *State) unblock() { s.Blocked = nil }

// clear 只解除指定原因；其他原因（例如跨版本延續的待確認）保留起算時間。
func (s *State) clear(reasons ...string) {
	if s.Blocked == nil {
		return
	}
	for _, r := range reasons {
		if s.Blocked.Reason == r {
			s.Blocked = nil
			return
		}
	}
}

func (s *State) failed(version string) bool {
	for _, v := range s.Failed {
		if v == version {
			return true
		}
	}
	return false
}

// Path 是狀態檔位置。
func Path(base string) string { return filepath.Join(base, "update", "state.json") }

// Load 讀取狀態；不存在時建立新的安裝識別碼與預設（自動）模式。
func Load(base string) (*State, error) {
	b, err := os.ReadFile(Path(base))
	if errors.Is(err, fs.ErrNotExist) {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return nil, err
		}
		return &State{Schema: 1, InstallID: hex.EncodeToString(id), Mode: ModeAuto, Stage: StageIdle}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Schema != 1 || s.InstallID == "" {
		return nil, errors.New("unsupported update state")
	}
	if s.Mode == "" {
		s.Mode = ModeAuto
	}
	if s.Stage == "" {
		s.Stage = StageIdle
	}
	return &s, nil
}

// Save 以暫存檔改名原子寫入。
func Save(base string, s *State) error {
	dir := filepath.Dir(Path(base))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), Path(base))
}

// ErrLocked：另一個程序正握著同一把鎖，短時間內拿不到。
var ErrLocked = filelock.ErrLocked

// WithLock 取得 base/update 下的命名鎖後執行 fn，用來序列化 state.json 以外的短暫讀改寫
// （例如 settings.json）。最多等 2 秒；前一次執行被中止留下的鎖會依行程是否存活接手。
func WithLock(base, name string, fn func() error) error {
	unlock, err := waitLock(context.Background(), base, name)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// waitLock waits only on contention; filesystem errors must retain their cause.
func waitLock(ctx context.Context, base, name string) (func(), error) {
	return filelock.Wait(ctx, base, name)
}

// ForgetInstalled 在使用者自己完整安裝後，清掉不比已安裝版本新的待辦。背景更新已準備好 dev.54、
// 使用者又用官網套件裝了 dev.54 時，選單與設定程式不該再顯示「立即安裝 dev.54」；協調器要等下一次
// 查詢才會丟棄它。切換中的核心與等待重新登入的同版外殼由協調器收尾，不在這裡動；等待重新登入的是
// 較舊的外殼（dev.59 待生效時手動裝了 dev.60）就清掉，不再顯示「等待重新登入後生效」（#103）。回傳是否清掉。
func ForgetInstalled(base, installed string) (bool, error) {
	if _, err := os.Stat(Path(base)); errors.Is(err, fs.ErrNotExist) {
		return false, nil // 第一次安裝，沒有待辦
	}
	dropped := false
	_, err := EditState(base, func(s *State) error {
		if s.Target == nil || s.Switched || newer(s.Target.Version, installed) ||
			(s.Target.Kind == "shell" && s.Stage == StagePending && s.Target.Version == installed) {
			return nil
		}
		s.record(time.Now(), "superseded", s.Target.Version, "已安裝 "+installed)
		s.Target, s.Download, s.Prepared, s.PreparedAt = nil, "", "", time.Time{}
		s.Snooze = nil
		s.Stage = StageIdle
		s.unblock()
		dropped = true
		return nil
	})
	return dropped, err
}

// EditState serializes short read-modify-write operations with coordinator
// saves. Preferences remain editable during a download; a slow transaction
// never lets an older preferences snapshot erase a newly persisted reminder.
func EditState(base string, change func(*State) error) (*State, error) {
	unlock, err := waitLock(context.Background(), base, "state-write.lock")
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := Load(base)
	if err != nil {
		return nil, err
	}
	if err = change(s); err != nil {
		return s, err
	}
	return s, Save(base, s)
}
